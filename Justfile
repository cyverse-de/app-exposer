default: build

build: docs app-exposer workflow-builder vice-export vice-import vice-launch vice-list

app-exposer:
    go build -o bin/app-exposer cmd/app-exposer/*.go

workflow-builder:
    go build -o bin/workflow-builder cmd/workflow-builder/*.go

vice-export:
    go build -o bin/vice-export cmd/vice-export/*.go

vice-import:
    go build -o bin/vice-import cmd/vice-import/*.go

vice-launch:
    go build -o bin/vice-launch cmd/vice-launch/*.go

vice-list:
    go build -o bin/vice-list cmd/vice-list/*.go

test-imageinfo:
    go test ./imageinfo

test-common:
    go test ./common

test: test-imageinfo test-common

fmt-docs:
    swag fmt -g app.go -d cmd/app-exposer/,httphandlers/,common/,incluster/

docs: fmt-docs
    swag init --parseDependency -g app.go -d cmd/app-exposer/,httphandlers/,common/,incluster/

clean:
    #!/usr/bin/env bash
    go clean
    if [ -f bin/app-exposer ]; then
        rm bin/app-exposer
    fi
    if [ -f bin/workflow-builder ]; then
        rm bin/workflow-builder
    fi
    if [ -f bin/vice-export ]; then
        rm bin/vice-export
    fi
    if [ -f bin/vice-import ]; then
        rm bin/vice-import
    fi
    if [ -f bin/vice-launch ]; then
        rm bin/vice-launch
    fi
    if [ -f bin/vice-list ]; then
        rm bin/vice-list
    fi
