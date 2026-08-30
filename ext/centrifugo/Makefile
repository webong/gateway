.PHONY: test test-go test-php smoke-docker smoke-kubernetes

ROOT := ../..
GOFLAGS ?= -mod=mod

test: test-go test-php

test-go:
	cd $(ROOT) && go test $(GOFLAGS) ./ext/centrifugo/runtime/...

test-php:
	../../vendor/bin/pest --configuration phpunit.xml.dist

smoke-docker:
	cd $(ROOT) && GATEWAY_CENTRIFUGO_DOCKER_SMOKE_IMAGE=$${GATEWAY_CENTRIFUGO_DOCKER_SMOKE_IMAGE:-centrifugo/centrifugo:v6} go test $(GOFLAGS) ./ext/centrifugo/runtime -run TestDockerSmoke -count=1

smoke-kubernetes:
	@namespace="gateway-centrifugo-smoke-$$$$"; \
	kubectl create namespace "$$namespace" >/dev/null; \
	trap 'kubectl delete namespace "$$namespace" --wait=false >/dev/null 2>&1 || true' EXIT; \
	cd $(ROOT) && GATEWAY_CENTRIFUGO_KUBERNETES_SMOKE_NAMESPACE="$$namespace" GATEWAY_CENTRIFUGO_KUBERNETES_SMOKE_IMAGE=$${GATEWAY_CENTRIFUGO_KUBERNETES_SMOKE_IMAGE:-centrifugo/centrifugo:v6} go test $(GOFLAGS) ./ext/centrifugo/runtime -run TestKubernetesSmoke -count=1
