
# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GO111MODULE=off
GOOS?=darwin
GOARCH=amd64

grpc:
	protoc -I ./protos/ -I ${GOPATH}/src --go_out=plugins=grpc:./protos ./protos/service.proto

twirp:
	protoc --go_out=. --twirp_out=. ./protos/service.proto

run-cluster:
	go run -race ./cmd/main.go

run-client:
	go run ./client/client.go

swagger:
	twirp-swagger-gen -in protos/service.proto -out docs/replicator.swagger.json -host localhost:9000