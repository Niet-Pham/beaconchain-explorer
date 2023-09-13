#!/usr/bin/env bash
export BIGTABLE_EMULATOR_HOST="localhost:8086"
INSTANCE="localhost:8086"
PROJECT="cleo_beaconchain_explorer"

cbt -project $PROJECT -instance $INSTANCE createtable beaconchain
cbt -project $PROJECT -instance $INSTANCE createtable blocks
cbt -project $PROJECT -instance $INSTANCE createtable cache
cbt -project $PROJECT -instance $INSTANCE createtable data
cbt -project $PROJECT -instance $INSTANCE createtable metadata
cbt -project $PROJECT -instance $INSTANCE createtable metadata_updates
cbt -project $PROJECT -instance $INSTANCE createtable machine_metrics

cbt -project $PROJECT -instance $INSTANCE createfamily beaconchain at
cbt -project $PROJECT -instance $INSTANCE createfamily beaconchain id
cbt -project $PROJECT -instance $INSTANCE createfamily beaconchain pr
cbt -project $PROJECT -instance $INSTANCE createfamily beaconchain sc
cbt -project $PROJECT -instance $INSTANCE createfamily beaconchain stats
cbt -project $PROJECT -instance $INSTANCE createfamily beaconchain vb

cbt -project $PROJECT -instance $INSTANCE createfamily blocks default

cbt -project $PROJECT -instance $INSTANCE createfamily cache 10_min
cbt -project $PROJECT -instance $INSTANCE createfamily cache 1_day
cbt -project $PROJECT -instance $INSTANCE createfamily cache 1_hour

cbt -project $PROJECT -instance $INSTANCE createfamily data c
cbt -project $PROJECT -instance $INSTANCE createfamily data f

cbt -project $PROJECT -instance $INSTANCE createfamily metadata a
cbt -project $PROJECT -instance $INSTANCE createfamily metadata c
cbt -project $PROJECT -instance $INSTANCE createfamily metadata erc1155
cbt -project $PROJECT -instance $INSTANCE createfamily metadata erc20
cbt -project $PROJECT -instance $INSTANCE createfamily metadata erc721
cbt -project $PROJECT -instance $INSTANCE createfamily metadata series

cbt -project $PROJECT -instance $INSTANCE createfamily metadata_updates blocks
cbt -project $PROJECT -instance $INSTANCE createfamily metadata_updates f

cbt -project $PROJECT -instance $INSTANCE createfamily machine_metrics mm