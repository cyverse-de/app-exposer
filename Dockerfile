### First stage
FROM quay.io/goswagger/swagger as swagger

FROM golang:1.17 as build-root

WORKDIR /build

ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64

COPY --from=swagger /usr/bin/swagger /usr/bin/

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

RUN go install -v ./...
RUN swagger generate spec -o ./docs/swagger.json --scan-models

ENTRYPOINT ["app-exposer"]

EXPOSE 60000
