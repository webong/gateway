.PHONY: build build-integration-manager build-with-script-extensions test test-go test-go-extensions test-runtime-module-boundaries test-automation-extension-boundaries test-integration-boundaries test-router-image-packaging test-router-companion-image test-planner-octane-servers test-arranger-release test-contract-package test-php test-roadrunner-planner-env test-octane test-http test-smtp test-dns test-dns-integration test-websocket test-webstream test-schedule-roadrunner test-frankenphp test-container test-container-integration benchmark benchmark-php vet

GOFLAGS ?= -mod=mod
# FrankenPHP-backed guests (php, hookscript) need ZTS PHP embed headers. The
# Docker build supplies php-config; local builds need the same toolchain.
GATEWAY_BUILD_TAGS ?= nowatcher,nobrotli,nomercure
GATEWAY_TAG_FLAGS = $(if $(strip $(GATEWAY_BUILD_TAGS)),-tags $(GATEWAY_BUILD_TAGS),)
GATEWAY_HOST_DIR = pkg/spinner/cmd/gateway

build:
	mkdir -p bin
	GOWORK=off CGO_ENABLED=1 \
		CGO_CFLAGS="$$(php-config --includes 2>/dev/null)" \
		CGO_LDFLAGS="$$(php-config --ldflags 2>/dev/null) $$(php-config --libs 2>/dev/null)" \
		go -C $(GATEWAY_HOST_DIR) build $(GOFLAGS) $(GATEWAY_TAG_FLAGS) -o $(CURDIR)/bin/gateway .

build-integration-manager:
	mkdir -p bin
	GOWORK=off CGO_ENABLED=0 go -C pkg/spinner build $(GOFLAGS) -o ../../bin/gateway-integration ./cmd/gateway-integration

build-with-script-extensions:
	$(MAKE) build

test: test-go test-php

test-go:
	GOWORK=off go -C mod/arranger test ./...
	GOWORK=off go -C pkg/caddy test ./...
	GOWORK=off go -C pkg/javascript test ./...
	GOWORK=off go -C pkg/lua test ./...
	GOWORK=off go -C pkg/shell test ./...
	GOWORK=off go -C pkg/reverb test ./...
	GOWORK=off go -C pkg/mercure test ./...
	GOWORK=off go -C pkg/centrifugo test ./...
	GOWORK=off go -C pkg/spinner test $(GOFLAGS) ./...
	GOWORK=off go -C tools/spinner test $(GOFLAGS) ./...
	$(MAKE) test-runtime-module-boundaries
	bash scripts/test-router-boundaries
	$(MAKE) test-router-image-packaging
	$(MAKE) test-go-extensions
	$(MAKE) test-automation-extension-boundaries
	$(MAKE) test-integration-boundaries
	$(MAKE) test-arranger-release

test-go-extensions:
	GOWORK=off go -C tools/spinner test $(GOFLAGS) ./...

test-runtime-module-boundaries:
	bash scripts/test-runtime-module-boundaries

test-automation-extension-boundaries:
	bash scripts/test-automation-extension-boundaries

test-integration-boundaries:
	bash scripts/test-integration-boundaries

test-router-image-packaging:
	bash scripts/test-router-image-packaging

test-router-companion-image:
	bash scripts/test-router-companion-image

test-planner-octane-servers:
	bash scripts/test-planner-octane-servers

test-arranger-release:
	bash scripts/test-arranger-release

test-contract-package:
	bash scripts/test-contract-package

test-php:
	$(MAKE) test-contract-package
	$(MAKE) test-roadrunner-planner-env
	vendor/bin/pest
	$(MAKE) -C pkg/reverb test-php
	$(MAKE) -C pkg/mercure test-php
	$(MAKE) -C pkg/centrifugo test-php
	$(MAKE) -C pkg/webhookscript test-php

test-roadrunner-planner-env:
	bash scripts/test-roadrunner-planner-env

test-octane:
	bash scripts/octane-smoke.sh

test-http:
	bash scripts/http-smoke.sh

test-smtp:
	bash scripts/smtp-smoke.sh

test-email:
	bash scripts/email-smoke.sh

test-dns:
	GOWORK=off go -C pkg/spinner test $(GOFLAGS) ./bridge/dns -run TestDNSServerServesUDPAndTCP -count=1

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
	GOWORK=off go -C tools run $(GOFLAGS) ./benchmark $(ARGS)

benchmark-php:
	vendor/bin/pest -c phpunit.benchmark.xml.dist

vet:
	GOWORK=off go -C mod/arranger vet ./...
	GOWORK=off go -C pkg/spinner vet $(GOFLAGS) ./...
