.PHONY: build test test-go test-php test-octane test-http benchmark benchmark-php vet

GOFLAGS ?= -mod=mod

build:
	mkdir -p bin
	CGO_ENABLED=0 go build $(GOFLAGS) -o bin/gateway ./cmd/proxy

test: test-go test-php

test-go:
	go test $(GOFLAGS) ./...

test-php:
	composer test

test-octane:
	bash scripts/octane-smoke.sh

test-http:
	bash scripts/http-smoke.sh

benchmark:
	go run $(GOFLAGS) ./cmd/benchmark $(ARGS)

benchmark-php:
	vendor/bin/pest -c phpunit.benchmark.xml.dist

vet:
	go vet $(GOFLAGS) ./...
