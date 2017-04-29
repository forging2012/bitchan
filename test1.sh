#!/bin/sh
set -e
go build
rm -rf tmp
mkdir -p tmp
cp bitchan tmp/
cd tmp && ./bitchan --gatewayPort=8081 --serventPort=8687
