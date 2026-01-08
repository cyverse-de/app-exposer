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

# Build the binary
ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64

RUN go build -ldflags='-w -s' -o app-exposer cmd/app-exposer/*.go

# Generate swagger documentation using swag (matches Justfile)
RUN swag init --parseDependency -g app.go -d cmd/app-exposer/,httphandlers/,common/,incluster/

### Final stage - minimal runtime image
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /

# Copy the binary from builder
COPY --from=builder /build/app-exposer /app-exposer

# Copy swagger documentation
COPY --from=builder /build/docs/swagger.json /docs/swagger.json

EXPOSE 60000

ENTRYPOINT ["/app-exposer"]
