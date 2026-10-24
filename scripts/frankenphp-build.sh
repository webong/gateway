#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BUILD_FLAGS="${GOFLAGS:--mod=mod}"

# Verify the optional Caddy integration as a Go module. Gateway no longer owns
# a dedicated FrankenPHP image; Planner runtime selection belongs to
# Gateway.planner.Dockerfile and the combined Router has its own neutral image.
GOWORK=off go -C "$ROOT/pkg/caddy" test "$GO_BUILD_FLAGS" ./...
echo 'Optional Caddy integration module tests passed.'
