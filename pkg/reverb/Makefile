.PHONY: test test-go test-php smoke smoke-kubernetes

ROOT := ../..
GOFLAGS ?= -mod=mod

test: test-go test-php

test-go:
	go test $(GOFLAGS) ./...

test-php:
	../../vendor/bin/pest --configuration phpunit.xml.dist

smoke:
	bash scripts/reverb-smoke.sh

smoke-kubernetes:
	bash scripts/reverb-kubernetes-smoke.sh
