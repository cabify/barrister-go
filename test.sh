#!/bin/bash

set -e 

go clean
go test -v

context_flags=( yes no both )
for context in "${context_flags[@]}"; do
	echo "Generating with -context=$context"
	go run idl2go/idl2go.go -context=$context -n -b "github.com/coopernurse/barrister-go/conform/generated/" -d conform/generated conform/conform.json
	conform/generate_server.sh $context conform/server.go
	go build conform/client.go
	go build conform/server.go
done
