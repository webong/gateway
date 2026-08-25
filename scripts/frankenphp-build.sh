#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BUILD_FLAGS="${GOFLAGS:--mod=mod}"

# Always verify the module on the host. The full binary build is optional
# because it requires FrankenPHP's CGO builder image and Docker access.
go test "$GO_BUILD_FLAGS" ./cmd/bridge/caddy

if [[ "${GATEWAY_FRANKENPHP_BUILD:-0}" != "1" ]]; then
	cat >&2 <<'EOF'
Caddy/FrankenPHP module compilation passed.
Set GATEWAY_FRANKENPHP_BUILD=1 to build and inspect the full FrankenPHP image
using docker/FrankenPHP.gateway.Dockerfile.
EOF
	exit 0
fi

if ! command -v docker >/dev/null 2>&1; then
	echo "Docker is required for GATEWAY_FRANKENPHP_BUILD=1." >&2
	exit 1
fi

if ! docker info >/dev/null 2>&1; then
	echo "Docker daemon is unavailable for the FrankenPHP build." >&2
	exit 1
fi

IMAGE="${GATEWAY_FRANKENPHP_IMAGE:-gateway-frankenphp:module-test}"
docker build --file "$ROOT/docker/FrankenPHP.gateway.Dockerfile" --tag "$IMAGE" "$ROOT"
docker run --rm "$IMAGE" frankenphp list-modules \
	| grep -E '(^|[[:space:]])(http\.handlers\.gateway|gateway\.smtp)($|[[:space:]])'
