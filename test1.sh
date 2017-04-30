#!/bin/sh
set -e
go build
rm -rf tmp
mkdir -p tmp
cp bitchan tmp/
cp *.html tmp/
cp *.gif tmp/
cd tmp && ./bitchan --gatewayPort=8081 --serventPort=8687 --dumpMessage
