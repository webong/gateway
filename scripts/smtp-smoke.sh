#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BUILD_FLAGS="${GOFLAGS:--mod=mod}"

# SMTP belongs to the installable Email Extension. This smoke builds the real
# DNS and Email packages, resolves Email's DNS dependency, launches both child
# processes through the generic Spinner host, sends an SMTP transaction, and
# verifies the durable delivery handoff.
GOWORK=off go -C "$ROOT/tools/spinner" test "$GO_BUILD_FLAGS" ./bridge \
    -run '^TestEmailCanBeInstalledWithDNSAfterSpinnerStarts$' -count=1
