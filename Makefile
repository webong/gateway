.PHONY: build build-extension-manager build-with-script-extensions test test-go test-go-extensions test-automation-extension-boundaries test-driver-release test-contract-package test-php test-octane test-http test-smtp test-dns test-dns-integration test-websocket test-webstream test-schedule-roadrunner test-frankenphp test-container test-container-integration benchmark benchmark-php vet

GOFLAGS ?= -mod=mod
GATEWAY_BUILD_TAGS ?=
GATEWAY_TAG_FLAGS = $(if $(strip $(GATEWAY_BUILD_TAGS)),-tags $(GATEWAY_BUILD_TAGS),)

build:
	mkdir -p bin
	CGO_ENABLED=1 go build $(GOFLAGS) $(GATEWAY_TAG_FLAGS) -o bin/gateway ./src/spinner/cmd/proxy

build-extension-manager:
	mkdir -p bin
	CGO_ENABLED=0 go build $(GOFLAGS) -o bin/gateway-extension ./src/spinner/cmd/gateway-extension

build-with-script-extensions:
	$(MAKE) build GATEWAY_BUILD_TAGS=gateway_javascript,gateway_lua

test: test-go test-php

test-go:
	go -C lib/driver test ./...
	go test $(GOFLAGS) ./...
	$(MAKE) test-go-extensions
	$(MAKE) test-automation-extension-boundaries
	$(MAKE) test-driver-release

test-go-extensions:
	go test $(GOFLAGS) -tags gateway_javascript,gateway_lua ./src/spinner/extensions/automation ./src/spinner/cmd/proxy

test-automation-extension-boundaries:
	bash scripts/test-automation-extension-boundaries

test-driver-release:
	bash scripts/test-driver-release

test-contract-package:
	bash scripts/test-contract-package

test-php:
	$(MAKE) test-contract-package
	composer test
	$(MAKE) -C ext/reverb test-php
	$(MAKE) -C ext/mercure test-php
	$(MAKE) -C ext/centrifugo test-php
	$(MAKE) -C ext/webhookscript test-php

test-octane:
	bash scripts/octane-smoke.sh

test-http:
	bash scripts/http-smoke.sh

test-smtp:
	bash scripts/smtp-smoke.sh

test-email:
	bash scripts/email-smoke.sh

test-dns:
	go test $(GOFLAGS) ./src/spinner/cmd/bridge/dns -run TestDNSServerServesUDPAndTCP -count=1

test-dns-integration:
	bash scripts/dns-smoke.sh

test-websocket:
	bash scripts/websocket-smoke.sh

test-webstream:
	bash scripts/webstream-smoke.sh

test-schedule-roadrunner:
	bash scripts/schedule-roadrunner-smoke.sh

test-frankenphp:
	bash scripts/frankenphp-build.sh

test-container:
	bash scripts/docker-smoke.sh

test-container-integration:
	bash scripts/docker-integration-smoke.sh

benchmark:
	go run $(GOFLAGS) ./src/spinner/cmd/benchmark $(ARGS)

benchmark-php:
	vendor/bin/pest -c phpunit.benchmark.xml.dist

vet:
	go -C lib/driver vet ./...
	go vet $(GOFLAGS) ./...
