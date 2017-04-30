#!/bin/sh
set -e
go build
#rm -rf bitchan.leveldb
./bitchan --initNodes=127.0.0.1:8687 $1
