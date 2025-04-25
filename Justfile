default: build

build: app-exposer workflow-builder

app-exposer: docs
    go build -o bin/app-exposer cmd/app-exposer/*.go

workflow-builder:
    go build -o bin/workflow-builder cmd/workflow-builder/*.go

test-imageinfo:
    go test ./imageinfo

test-common:
    go test ./common

test: test-imageinfo test-common

docs:
    swag fmt -g app.go -d cmd/app-exposer/,httphandlers/
    swag init -g app.go -d cmd/app-exposer/,httphandlers/

clean:
    #!/usr/bin/env bash
    go clean
    if [ -f bin/app-exposer ]; then
        rm bin/app-exposer
    fi
    if [ -f bin/workflow-builder ]; then
        rm bin/workflow-builder
    fi
