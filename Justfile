default: build

build: docs app-exposer deployer workflow-builder vice-cluster-admin

app-exposer:
    go build -o bin/app-exposer ./cmd/app-exposer

deployer:
    go build -o bin/vice-deployer ./cmd/deployer

deployer-lambda:
    go build -tags lambda -o bin/vice-deployer-lambda ./cmd/deployer

workflow-builder:
    go build -o bin/workflow-builder ./cmd/workflow-builder

vice-cluster-admin:
    go build -o bin/vice-cluster-admin ./cmd/vice-cluster-admin

test-imageinfo:
    go test ./imageinfo

test-common:
    go test ./common

test-coordinator:
    go test ./coordinator/...

test-deployer:
    go test ./deployer/...

test-vicetypes:
    go test ./vicetypes/...

test: test-imageinfo test-common test-coordinator test-deployer test-vicetypes

fmt-docs:
    swag fmt -g app.go -d cmd/app-exposer/,httphandlers/,common/,incluster/,coordinator/,deployer/,vicetypes/

docs: fmt-docs
    swag init --parseDependency -g app.go -d cmd/app-exposer/,httphandlers/,common/,incluster/,coordinator/,deployer/,vicetypes/

# Docker build targets
docker-app-exposer:
    docker build --target app-exposer -t app-exposer:latest .

docker-deployer:
    docker build --target deployer -t deployer:latest .

docker-deployer-lambda:
    docker build --target deployer-lambda -t deployer-lambda:latest .

docker-all: docker-app-exposer docker-deployer

clean:
    #!/usr/bin/env bash
    go clean
    if [ -f bin/app-exposer ]; then
        rm bin/app-exposer
    fi
    if [ -f bin/vice-deployer ]; then
        rm bin/vice-deployer
    fi
    if [ -f bin/vice-deployer-lambda ]; then
        rm bin/vice-deployer-lambda
    fi
    if [ -f bin/workflow-builder ]; then
        rm bin/workflow-builder
    fi
    if [ -f bin/vice-cluster-admin ]; then
        rm bin/vice-cluster-admin
    fi
