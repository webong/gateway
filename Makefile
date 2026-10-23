.PHONY: test test-go test-php smoke-docker smoke-kubernetes

ROOT := ../..
GOFLAGS ?= -mod=mod

test: test-go test-php

test-go:
	go test $(GOFLAGS) ./...

test-php:
	../../vendor/bin/pest --configuration phpunit.xml.dist

smoke-docker:
	GATEWAY_MERCURE_DOCKER_SMOKE_IMAGE=$${GATEWAY_MERCURE_DOCKER_SMOKE_IMAGE:-dunglas/mercure:latest} go test $(GOFLAGS) ./runtime -run TestDockerSmoke -count=1

smoke-kubernetes:
	@namespace="gateway-mercure-smoke-$$$$"; \
	kubectl create namespace "$$namespace" >/dev/null; \
	trap 'kubectl delete namespace "$$namespace" --wait=false >/dev/null 2>&1 || true' EXIT; \
	GATEWAY_MERCURE_KUBERNETES_SMOKE_NAMESPACE="$$namespace" GATEWAY_MERCURE_KUBERNETES_SMOKE_IMAGE=$${GATEWAY_MERCURE_KUBERNETES_SMOKE_IMAGE:-dunglas/mercure:latest} go test $(GOFLAGS) ./runtime -run TestKubernetesSmoke -count=1
