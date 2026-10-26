#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BUILD_FLAGS="${GOFLAGS:--mod=mod}"

# Keep local package/process verification distinct from the Kubernetes artifact
# smoke. The latter exercises Planner /extensions distribution, rollout, and
# the production container boundary.
bash "$ROOT/scripts/smtp-smoke.sh"
GOWORK=off go -C "$ROOT/pkg/email" test "$GO_BUILD_FLAGS" ./...
"$ROOT/vendor/bin/pest" --compact \
    "$ROOT/tests/Feature/EmailControlPlaneTest.php" \
    "$ROOT/tests/Feature/EmailDkimKeyTest.php" \
    "$ROOT/tests/Feature/EmailSrsPolicyTest.php" \
    "$ROOT/tests/Feature/EmailSubmissionPolicyTest.php"
