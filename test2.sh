#!/bin/sh
set -e
go build
./bitchan --initNodes=127.0.0.1:8687
