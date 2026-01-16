### Build stage
FROM golang:1.25 AS builder

WORKDIR /build

# Install swag for swagger documentation generation
RUN go install github.com/swaggo/swag/cmd/swag@latest

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build environment
ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64

# Build app-exposer (coordinator)
RUN go build -ldflags='-w -s' -o app-exposer ./cmd/app-exposer

# Build deployer (standalone mode - no Lambda support)
# Using ./cmd/deployer instead of *.go to respect build tags
RUN go build -ldflags='-w -s' -o vice-deployer ./cmd/deployer

# Build workflow-builder
RUN go build -ldflags='-w -s' -o workflow-builder ./cmd/workflow-builder

# Build vice-cluster-admin (CLI tool)
RUN go build -ldflags='-w -s' -o vice-cluster-admin ./cmd/vice-cluster-admin

# Generate swagger documentation
RUN swag init --parseDependency -g app.go -d cmd/app-exposer/,httphandlers/,common/,incluster/,coordinator/,deployer/,vicetypes/

### Lambda build stage (optional - for AWS Lambda deployments)
FROM builder AS lambda-builder
# Add AWS dependencies for Lambda build
RUN go get github.com/aws/aws-lambda-go/events github.com/aws/aws-lambda-go/lambda
RUN go get github.com/aws/aws-sdk-go-v2/config github.com/aws/aws-sdk-go-v2/service/secretsmanager
RUN go build -tags lambda -ldflags='-w -s' -o vice-deployer-lambda ./cmd/deployer


### Final stage - app-exposer (default)
FROM gcr.io/distroless/static-debian12:nonroot AS app-exposer

WORKDIR /

# Copy the binary from builder
COPY --from=builder /build/app-exposer /app-exposer

# Copy swagger documentation
COPY --from=builder /build/docs/swagger.json /docs/swagger.json

EXPOSE 60000

ENTRYPOINT ["/app-exposer"]


### Final stage - deployer (standalone mode)
FROM gcr.io/distroless/static-debian12:nonroot AS deployer

WORKDIR /

# Copy the binary from builder
COPY --from=builder /build/vice-deployer /vice-deployer

EXPOSE 8080

ENTRYPOINT ["/vice-deployer"]


### Final stage - deployer with Lambda support
FROM public.ecr.aws/lambda/provided:al2023 AS deployer-lambda

# Copy the Lambda-enabled binary
COPY --from=lambda-builder /build/vice-deployer-lambda /var/runtime/bootstrap

# Lambda handler is built into the binary
CMD ["handler"]


### Final stage - vice-cluster-admin (CLI tool)
FROM gcr.io/distroless/static-debian12:nonroot AS vice-cluster-admin

WORKDIR /

# Copy the binary from builder
COPY --from=builder /build/vice-cluster-admin /vice-cluster-admin

ENTRYPOINT ["/vice-cluster-admin"]


### Default target (app-exposer for backward compatibility)
FROM app-exposer
