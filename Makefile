.PHONY: build test test-go test-php test-octane vet

GOFLAGS ?= -mod=mod

build:
	mkdir -p bin
	CGO_ENABLED=0 go build $(GOFLAGS) -o bin/web-relay ./cmd/relayer

test: test-go test-php

test-go:
	go test $(GOFLAGS) ./...

test-php:
	composer test

test-octane:
	bash scripts/octane-smoke.sh

vet:
	go vet $(GOFLAGS) ./...
