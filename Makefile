BINARY_NAME := meshproxy

.PHONY: all build run clean fmt tidy proto

all: build

build:
	go build -o bin/$(BINARY_NAME) ./cmd/node

run: build
	./bin/$(BINARY_NAME) -config configs/config.example.yaml

fmt:
	go fmt ./...

tidy:
	go mod tidy

proto:
	protoc --go_out=./ --go_opt=module=meshproxy \
		--go-grpc_out=./ --go-grpc_opt=module=meshproxy \
		proto/meshproxy.proto

clean:
	rm -rf bin

