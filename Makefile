.PHONY: build test test-go test-php test-octane test-http test-smtp test-dns test-websocket test-frankenphp test-container test-container-integration benchmark benchmark-php vet

GOFLAGS ?= -mod=mod

build:
	mkdir -p bin
	CGO_ENABLED=0 go build $(GOFLAGS) -o bin/gateway ./cmd/proxy

test: test-go test-php

test-go:
	go test $(GOFLAGS) ./...

test-php:
	composer test
	$(MAKE) -C ext/reverb test-php
	$(MAKE) -C ext/mercure test-php
	$(MAKE) -C ext/centrifugo test-php

test-octane:
	bash scripts/octane-smoke.sh

test-http:
	bash scripts/http-smoke.sh

test-smtp:
	bash scripts/smtp-smoke.sh

test-dns:
	go test $(GOFLAGS) ./cmd/bridge/dns -run TestDNSServerServesUDPAndTCP -count=1

test-websocket:
	bash scripts/websocket-smoke.sh

test-frankenphp:
	bash scripts/frankenphp-build.sh

test-container:
	bash scripts/docker-smoke.sh

test-container-integration:
	bash scripts/docker-integration-smoke.sh

benchmark:
	go run $(GOFLAGS) ./cmd/benchmark $(ARGS)

benchmark-php:
	vendor/bin/pest -c phpunit.benchmark.xml.dist

vet:
	go vet $(GOFLAGS) ./...
