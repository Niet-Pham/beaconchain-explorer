package exporter

import (
	"bytes"
	"context"
	"eth2-exporter/cache"
	"eth2-exporter/db"
	"eth2-exporter/types"
	"eth2-exporter/utils"
	"fmt"
	"sort"
	"sync"
	"time"

	ptypes "github.com/golang/protobuf/ptypes/empty"
	ethpb "github.com/prysmaticlabs/ethereumapis/eth/v1alpha1"
	"github.com/sirupsen/logrus"
)

var logger = logrus.New().WithField("module", "exporter")

func Start(client ethpb.BeaconChainClient) error {

	if utils.Config.Indexer.FullIndexOnStartup {
		logger.Printf("Performing one time full db reindex")
		head, err := client.GetChainHead(context.Background(), &ptypes.Empty{})
		if err != nil {
			logger.Fatal(err)
		}

		var wg sync.WaitGroup
		for epoch := uint64(0); epoch <= head.HeadBlockEpoch; epoch++ {
			//err := exportEpoch(epoch, client)

			if err != nil {
				logger.Fatal(err)
			}
			wg.Add(1)

			logger.Printf("Exporting epoch %v of %v", epoch, head.HeadBlockEpoch)
			go func(e uint64) {
				err := exportEpoch(e, client)

				if err != nil {
					logger.Fatal(err)
				}
				logger.Printf("Finished export for epoch %v", e)
				wg.Done()
			}(epoch)

			if epoch%10 == 0 {
				logger.Printf("Waiting...")
				wg.Wait()
			}
		}
	}

	for true {

		head, err := client.GetChainHead(context.Background(), &ptypes.Empty{})
		if err != nil {
			logger.Fatal(err)
		}

		dbBlocks, err := db.GetLastPendingAndProposedBlocks(head.FinalizedEpoch, head.HeadBlockEpoch)
		if err != nil {
			logger.Fatal(err)
		}

		nodeBlocks, err := getLastBlocks(head.FinalizedEpoch, head.HeadBlockEpoch, client)
		if err != nil {
			logger.Fatal(err)
		}

		blocksMap := make(map[uint64]*types.BlockComparisonContainer)

		for _, block := range dbBlocks {
			_, found := blocksMap[block.Slot]

			if !found {
				blocksMap[block.Slot] = &types.BlockComparisonContainer{Epoch: block.Epoch}
			}

			blocksMap[block.Slot].Db = block
		}
		for _, block := range nodeBlocks {
			_, found := blocksMap[block.Slot]

			if !found {
				blocksMap[block.Slot] = &types.BlockComparisonContainer{Epoch: block.Epoch}
			}

			blocksMap[block.Slot].Node = block
		}

		epochsToExport := make(map[uint64]bool)

		for slot, block := range blocksMap {
			if block.Db == nil {
				logger.Printf("Queuing epoch %v for export as block %v is present on the node but missing in the db", block.Epoch, slot)
				epochsToExport[block.Epoch] = true
			} else if block.Node == nil {
				logger.Printf("Queuing epoch %v for export as block %v is present on the db but missing in the node", block.Epoch, slot)
				epochsToExport[block.Epoch] = true
			} else if bytes.Compare(block.Db.BockRoot, block.Node.BockRoot) != 0 {
				logger.Printf("Queuing epoch %v for export as block %v has a different hash in the db as on the node", block.Epoch, slot)
				epochsToExport[block.Epoch] = true
			}
		}

		// Add any missing epoch to the export set (might happen if the indexer was stopped for a long period of time)
		epochs, err := db.GetAllEpochs()
		if err != nil {
			logger.Fatal(err)
		}

		for i := 0; i < len(epochs)-1; i++ {
			if epochs[i] != epochs[i+1]-1 && epochs[i] != epochs[i+1] {
				logger.Println("Epochs between", epochs[i], "and", epochs[i+1], "are missing!")

				for j := epochs[i]; j <= epochs[i+1]; j++ {
					epochsToExport[j] = true
				}
			}
		}

		// Add not yet exported epochs to the export set (for example during the initial sync)
		if len(epochs) > 0 && epochs[len(epochs)-1] < head.HeadBlockEpoch {
			for i := epochs[len(epochs)-1]; i <= head.HeadBlockEpoch; i++ {
				epochsToExport[i] = true
			}
		} else if len(epochs) > 0 && epochs[0] != 0 { // Export the genesis epoch if not yet present in the db
			epochsToExport[0] = true
		} else if len(epochs) == 0 { // No epochs are present int the db
			for i := uint64(0); i <= head.HeadBlockEpoch; i++ {
				epochsToExport[i] = true
			}
		}

		logger.Printf("Exporting %v epochs.", len(epochsToExport))

		keys := make([]uint64, 0)
		for k, _ := range epochsToExport {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			return keys[i] < keys[j]
		})

		for _, epoch := range keys {
			logger.Printf("Exporting epoch %v", epoch)
			err = exportEpoch(epoch, client)

			if err != nil {
				logger.Fatal(err)
			}
			logger.Printf("Finished export for epoch %v", epoch)
		}

		// Update epoch statistics up to 10 epochs after the last finalized epoch
		startEpoch := uint64(0)
		if head.FinalizedEpoch > 10 {
			startEpoch = head.FinalizedEpoch - 10
		}
		err = updateEpochStatus(client, startEpoch, head.HeadBlockEpoch)
		if err != nil {
			logger.Fatal(err)
		}

		err = exportAttestationPool(client)
		if err != nil {
			logger.Fatal(err)
		}

		err = exportValidatorQueue(client)
		if err != nil {
			logger.Fatal(err)
		}

		time.Sleep(time.Second * 10)
	}

	return nil
}

func getLastBlocks(startEpoch, endEpoch uint64, client ethpb.BeaconChainClient) ([]*types.MinimalBlock, error) {
	var err error
	blocks := make([]*types.MinimalBlock, 0)

	for i := startEpoch; i <= endEpoch; i++ {
		blocksResponse := &ethpb.ListBlocksResponse{}
		for blocksResponse.NextPageToken == "" || len(blocksResponse.BlockContainers) >= utils.PageSize {
			blocksResponse, err = client.ListBlocks(context.Background(), &ethpb.ListBlocksRequest{PageToken: blocksResponse.NextPageToken, PageSize: utils.PageSize, QueryFilter: &ethpb.ListBlocksRequest_Epoch{Epoch: i}})
			if err != nil {
				logger.Fatal(err)
			}
			if blocksResponse.TotalSize == 0 {
				break
			}

			for _, block := range blocksResponse.BlockContainers {
				blocks = append(blocks, &types.MinimalBlock{
					Epoch:    i,
					Slot:     block.Block.Slot,
					BockRoot: block.BlockRoot,
				})
			}
		}
	}

	return blocks, nil
}

func exportEpoch(epoch uint64, client ethpb.BeaconChainClient) error {
	var err error

	data := &types.EpochData{}
	data.Epoch = epoch

	data.ValidatorAssignmentes, err = cache.GetEpochAssignments(epoch)
	if err != nil {
		return fmt.Errorf("error retrieving assignments for epoch %v: %v", epoch, err)
	}
	logger.Printf("Retrieved data for %v validators for epoch %v", len(data.Validators), epoch)

	// Retrieve all blocks for the epoch
	data.Blocks = make(map[uint64]*types.BlockContainer)
	blocksResponse := &ethpb.ListBlocksResponse{}
	for blocksResponse.NextPageToken == "" || len(blocksResponse.BlockContainers) >= utils.PageSize {
		blocksResponse, err = client.ListBlocks(context.Background(), &ethpb.ListBlocksRequest{PageToken: blocksResponse.NextPageToken, PageSize: utils.PageSize, QueryFilter: &ethpb.ListBlocksRequest_Epoch{Epoch: epoch}})
		if err != nil {
			logger.Fatal(err)
		}
		if blocksResponse.TotalSize == 0 {
			break
		}

		for _, block := range blocksResponse.BlockContainers {

			// Make sure that blocks from the genesis epoch have their Eth1Data field set
			if epoch == 0 && block.Block.Body.Eth1Data == nil {
				block.Block.Body.Eth1Data = &ethpb.Eth1Data{
					DepositRoot:  []byte{},
					DepositCount: 0,
					BlockHash:    []byte{},
				}
			}

			data.Blocks[block.Block.Slot] = &types.BlockContainer{
				Status:   1,
				Proposer: data.ValidatorAssignmentes.ProposerAssignments[block.Block.Slot],
				Block:    block,
			}
		}
	}
	logger.Printf("Retrieved %v blocks for epoch %v", len(data.Blocks), epoch)

	// Retrieve the validator set for the epoch
	data.Validators = make([]*ethpb.Validator, 0)

	validatorResponse := &ethpb.Validators{}
	for validatorResponse.NextPageToken == "" || (len(validatorResponse.Validators) >= utils.PageSize) {
		validatorResponse, err = client.ListValidators(context.Background(), &ethpb.ListValidatorsRequest{PageToken: validatorResponse.NextPageToken, PageSize: utils.PageSize, QueryFilter: &ethpb.ListValidatorsRequest_Epoch{Epoch: epoch}})
		if err != nil {
			logger.Printf("error retrieving validator response: %v", err)
			break
		}
		if validatorResponse.TotalSize == 0 {
			break
		}

		for _, validator := range validatorResponse.Validators {
			data.Validators = append(data.Validators, validator)
		}

	}

	for slot, proposer := range data.ValidatorAssignmentes.ProposerAssignments {
		_, found := data.Blocks[slot]
		if !found {
			// Proposer was assigned but did not yet propose a block

			data.Blocks[slot] = &types.BlockContainer{
				Status:   0,
				Proposer: proposer,
				Block: &ethpb.BeaconBlockContainer{
					Block: &ethpb.BeaconBlock{
						Slot:       slot,
						ParentRoot: []byte{},
						StateRoot:  []byte{},
						Body: &ethpb.BeaconBlockBody{
							RandaoReveal: []byte{},
							Eth1Data: &ethpb.Eth1Data{
								DepositRoot:  []byte{},
								DepositCount: 0,
								BlockHash:    []byte{},
							},
							Graffiti:          []byte{},
							ProposerSlashings: []*ethpb.ProposerSlashing{},
							AttesterSlashings: []*ethpb.AttesterSlashing{},
							Attestations:      []*ethpb.Attestation{},
							Deposits:          []*ethpb.Deposit{},
							VoluntaryExits:    []*ethpb.VoluntaryExit{},
						},
						Signature: []byte{},
					},
				},
			}

			if utils.SlotToTime(slot).After(time.Now().Add(time.Second * -60)) {
				// Block is in the future, set status to scheduled
				data.Blocks[slot].Status = 0
				data.Blocks[slot].Block.BlockRoot = []byte{0x0}
			} else {
				// Block is in the past, set status to missed
				data.Blocks[slot].Status = 2
				data.Blocks[slot].Block.BlockRoot = []byte{0x1}
			}
		} else {
			data.Blocks[slot].Proposer = proposer
		}
	}

	logger.Printf("Retrieved data for %v assignments for epoch %v", len(data.ValidatorAssignmentes.ProposerAssignments), epoch)

	// Retrieve the beacon committees for the epoch
	data.BeaconCommittees = make([]*ethpb.BeaconCommittees_CommitteeItem, 0)
	beaconCommitteesResponse := &ethpb.BeaconCommittees{}
	for beaconCommitteesResponse.NextPageToken == "" || len(beaconCommitteesResponse.Committees) >= utils.PageSize {
		beaconCommitteesResponse, err = client.ListBeaconCommittees(context.Background(), &ethpb.ListCommitteesRequest{PageToken: beaconCommitteesResponse.NextPageToken, PageSize: utils.PageSize, QueryFilter: &ethpb.ListCommitteesRequest_Epoch{Epoch: epoch}})
		if err != nil {
			logger.Printf("error retrieving beacon committees response: %v", err)
			break
		}
		if beaconCommitteesResponse.TotalSize == 0 {
			break
		}

		data.BeaconCommittees = append(data.BeaconCommittees, beaconCommitteesResponse.Committees...)
	}

	// Retrieve the validator balances for the epoch (NOTE: Currently the API call is broken and allows only to retrieve the balances for the current epoch
	data.ValidatorBalances = make([]*ethpb.ValidatorBalances_Balance, 0)
	validatorBalancesResponse := &ethpb.ValidatorBalances{}
	for validatorBalancesResponse.NextPageToken == "" || len(validatorBalancesResponse.Balances) >= utils.PageSize {
		validatorBalancesResponse, err = client.ListValidatorBalances(context.Background(), &ethpb.ListValidatorBalancesRequest{PageToken: validatorBalancesResponse.NextPageToken, PageSize: utils.PageSize, QueryFilter: &ethpb.ListValidatorBalancesRequest_Epoch{Epoch: epoch}})
		if err != nil {
			logger.Printf("error retrieving validator balances response: %v", err)
			break
		}
		if validatorBalancesResponse.TotalSize == 0 {
			break
		}

		for _, balance := range validatorBalancesResponse.Balances {
			data.ValidatorBalances = append(data.ValidatorBalances, balance)
		}
	}
	logger.Printf("Retrieved data for %v validator balances for epoch %v", len(data.ValidatorBalances), epoch)

	data.EpochParticipationStats, err = client.GetValidatorParticipation(context.Background(), &ethpb.GetValidatorParticipationRequest{QueryFilter: &ethpb.GetValidatorParticipationRequest_Epoch{Epoch: epoch}})
	if err != nil {
		logger.Printf("error retrieving epoch participation statistics: %v", err)
		data.EpochParticipationStats = &ethpb.ValidatorParticipationResponse{
			Epoch:         epoch,
			Finalized:     true,
			Participation: &ethpb.ValidatorParticipation{},
		}
	}

	return db.SaveEpoch(data)
}

func exportAttestationPool(client ethpb.BeaconChainClient) error {
	attestations, err := client.AttestationPool(context.Background(), &ptypes.Empty{})

	if err != nil {
		return fmt.Errorf("error retrieving attestation pool data: %v", err)
	}

	logger.Printf("Retrieved %v attestations from the attestation pool", len(attestations.Attestations))

	return db.SaveAttestationPool(attestations.Attestations)
}

func exportValidatorQueue(client ethpb.BeaconChainClient) error {
	validators, err := client.GetValidatorQueue(context.Background(), &ptypes.Empty{})

	if err != nil {
		return fmt.Errorf("error retrieving validator queue data: %v", err)
	}

	logger.Printf("Retrieved %v validators to enter and %v validators to leave from the validator queue", len(validators.ActivationPublicKeys), len(validators.ExitPublicKeys))

	return db.SaveValidatorQueue(validators)
}

func updateEpochStatus(client ethpb.BeaconChainClient, startEpoch, endEpoch uint64) error {
	for epoch := startEpoch; epoch <= endEpoch; epoch++ {
		epochParticipationStats, err := client.GetValidatorParticipation(context.Background(), &ethpb.GetValidatorParticipationRequest{QueryFilter: &ethpb.GetValidatorParticipationRequest_Epoch{Epoch: epoch}})
		if err != nil {
			logger.Printf("error retrieving epoch participation statistics: %v", err)
		} else {
			logger.Printf("Updating epoch %v with status finalized = %v", epoch, epochParticipationStats.Finalized)
			err := db.UpdateEpochStatus(epochParticipationStats)

			if err != nil {
				return err
			}
		}
	}
	return nil
}
