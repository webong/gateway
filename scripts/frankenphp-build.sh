#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BUILD_FLAGS="${GOFLAGS:--mod=mod}"

# Always verify the module on the host. The full binary build is optional
# because it requires FrankenPHP's CGO builder image and Docker access.
GOWORK=off go -C "$ROOT/pkg/caddy" test "$GO_BUILD_FLAGS" ./...

if [[ "${GATEWAY_FRANKENPHP_BUILD:-0}" != "1" ]]; then
	cat >&2 <<'EOF'
Caddy/FrankenPHP module compilation passed.
Set GATEWAY_FRANKENPHP_BUILD=1 to build and inspect the full FrankenPHP image
using infra/docker/FrankenPHP.gateway.Dockerfile.
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
docker build --file "$ROOT/infra/docker/FrankenPHP.gateway.Dockerfile" --tag "$IMAGE" "$ROOT"
modules="$(docker run --rm "$IMAGE" frankenphp list-modules)"
for module in http.handlers.gateway gateway.smtp gateway.pop3 gateway.imap; do
	if ! grep -Fqx "$module" <<< "$modules"; then
		echo "FrankenPHP image is missing Gateway module $module." >&2
		exit 1
	fi
done
adapted="$(docker run --rm --entrypoint frankenphp \
	-v "$ROOT/infra/caddy/Caddyfile.mailbox.integration:/etc/caddy/Caddyfile:ro" \
	"$IMAGE" adapt --config /etc/caddy/Caddyfile --adapter caddyfile)"
for module in gateway.pop3 gateway.imap; do
	if ! grep -Fq "\"$module\"" <<< "$adapted"; then
		echo "FrankenPHP did not adapt the $module Caddyfile option." >&2
		exit 1
	fi
done
echo 'FrankenPHP image includes Gateway HTTP, SMTP, POP3, and IMAP modules.'
