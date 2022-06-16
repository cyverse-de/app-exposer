### First stage
FROM quay.io/goswagger/swagger as swagger

FROM golang:1.18 as build-root

WORKDIR /go/src/github.com/cyverse-de/app-exposer
COPY . . 

ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64

RUN go build --buildvcs=false .
RUN go clean -cache -modcache
RUN cp ./app-exposer /bin/app-exposer

COPY --from=swagger /usr/bin/swagger /usr/bin/
RUN swagger generate spec -o ./docs/swagger.json --scan-models

ENTRYPOINT ["app-exposer"]

EXPOSE 60000
