#!/usr/bin/env bash
set -euo pipefail
root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
project="gateway-workflow-test-$$"
export WORKFLOW_TEST_IMAGE="$project-test"
compose=(docker compose -p "$project" -f "$root/pkg/weaver/compose.test.yml")
trap '"${compose[@]}" down --volumes --remove-orphans >/dev/null' EXIT
"${compose[@]}" build test
"${compose[@]}" run --rm test "$@"
python3 "$root/pkg/weaver/tests/http-smoke.py" "$WORKFLOW_TEST_IMAGE"
python3 "$root/pkg/weaver/tests/cutover-smoke.py" "$WORKFLOW_TEST_IMAGE"
