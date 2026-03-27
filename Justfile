default: build

build: docs operator-docs app-exposer vice-operator workflow-builder vice-export vice-import vice-launch vice-list vice-bundle vice-userid

app-exposer:
    go build -o bin/app-exposer cmd/app-exposer/*.go

vice-operator:
    go build -o bin/vice-operator cmd/vice-operator/*.go

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

vice-bundle:
    go build -o bin/vice-bundle cmd/vice-bundle/*.go

vice-userid:
    go build -o bin/vice-userid cmd/vice-userid/*.go

test-imageinfo:
    go test ./imageinfo

test-common:
    go test ./common

test-operator:
    go test ./operator/...

test-operatorclient:
    go test ./operatorclient/...

test: test-imageinfo test-common test-operator test-operatorclient

fmt-docs:
    swag fmt -g app.go -d cmd/app-exposer/,httphandlers/,common/,incluster/

docs: fmt-docs
    swag init --parseDependency -g app.go -d cmd/app-exposer/,httphandlers/,common/,incluster/

fmt-operator-docs:
    swag fmt -g app.go -d cmd/vice-operator/,operator/,operatorclient/,common/

operator-docs: fmt-operator-docs
    swag init --parseDependency -g app.go -d cmd/vice-operator/,operator/,operatorclient/,common/ -o operatordocs/ --instanceName operator --td '[{,}]'

clean:
    #!/usr/bin/env bash
    go clean
    if [ -f bin/app-exposer ]; then
        rm bin/app-exposer
    fi
    if [ -f bin/vice-operator ]; then
        rm bin/vice-operator
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
    if [ -f bin/vice-bundle ]; then
        rm bin/vice-bundle
    fi
    if [ -f bin/vice-userid ]; then
        rm bin/vice-userid
    fi
