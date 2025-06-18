default: build

build: docs app-exposer workflow-builder workflow-cleanup

app-exposer:
    go build -o bin/app-exposer cmd/app-exposer/*.go

workflow-builder:
    go build -o bin/workflow-builder cmd/workflow-builder/*.go

workflow-cleanup:
    go build -o bin/workflow-cleanup cmd/workflow-cleanup/*.go

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
    if [ -f bin/workflow-cleanup ]; then
        rm bin/workflow-cleanup
    fi
