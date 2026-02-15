.PHONY: build test tidy run

build:
	mkdir -p bin
	go build -o bin/awscope ./cmd/awscope

test:
	go test ./...

tidy:
	go mod tidy

run:
	go run ./cmd/awscope
