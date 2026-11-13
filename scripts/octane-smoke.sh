#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_PATH="${GATEWAY_OCTANE_APP_PATH:-$ROOT/tests/Fixtures/octane-app}"
PORT="${GATEWAY_OCTANE_PORT:-18080}"
TOKEN="${GATEWAY_INTERNAL_TOKEN:-octane-smoke-token}"
LOG_LEVEL="${GATEWAY_OCTANE_LOG_LEVEL:-error}"
BASE_URL="http://127.0.0.1:$PORT"

TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/gateway-octane.XXXXXX")"
CONFIG="$TEMP_DIR/config.yaml"
LOG="$TEMP_DIR/relay.log"
BODY="$TEMP_DIR/response.body"
RELAY_BIN="$TEMP_DIR/gateway"
RELAY_PID=""

cleanup() {
    if [[ -n "$RELAY_PID" ]] && kill -0 "$RELAY_PID" 2>/dev/null; then
        kill "$RELAY_PID" 2>/dev/null || true
        wait "$RELAY_PID" 2>/dev/null || true
    fi

    rm -f "$CONFIG" "$LOG" "$BODY" "$RELAY_BIN"
    rmdir "$TEMP_DIR" 2>/dev/null || true
}

trap cleanup EXIT INT TERM

if [[ ! -f "$APP_PATH/bootstrap/app.php" ]]; then
    echo "Laravel bootstrap not found: $APP_PATH/bootstrap/app.php" >&2
    exit 1
fi

if [[ ! -x "$ROOT/vendor/bin/roadrunner-worker" ]]; then
    echo "RoadRunner worker not found: $ROOT/vendor/bin/roadrunner-worker" >&2
    echo "Install the Composer dependencies before running this smoke test." >&2
    exit 1
fi

cat > "$CONFIG" <<EOF
version: "3"

server:
  command: "php $ROOT/vendor/bin/roadrunner-worker"
  env:
    APP_BASE_PATH: "$APP_PATH"
    COMPOSER_VENDOR_DIR: "$ROOT/vendor"
    APP_ENV: "testing"
    APP_KEY: "base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
    LARAVEL_OCTANE: "1"
    GATEWAY_INTERNAL_TOKEN: "$TOKEN"

http:
  address: "127.0.0.1:$PORT"
  middleware: ["gateway", "gzip"]
  max_request_size: 10
  pool:
    num_workers: 1

logs:
  level: "$LOG_LEVEL"
EOF

GO_BUILD_FLAGS="${GOFLAGS:--mod=mod}"
# The repository's go.work rejects -mod=mod, so build the module on its own, as CI does.
GOWORK=off go -C "$ROOT/pkg/roadrunner/cmd/router" build "$GO_BUILD_FLAGS" -o "$RELAY_BIN" .

ROADRUNNER_CONFIG="$CONFIG" \
GATEWAY_INTERNAL_TOKEN="$TOKEN" \
"$RELAY_BIN" >"$LOG" 2>&1 &
RELAY_PID=$!

ready="false"
for _ in $(seq 1 60); do
    if curl -fsS --max-time 1 "$BASE_URL/octane-smoke/worker" >"$BODY" 2>/dev/null; then
        ready="true"
        break
    fi

    if ! kill -0 "$RELAY_PID" 2>/dev/null; then
        cat "$LOG" >&2
        exit 1
    fi

    sleep 0.25
done

if [[ "$ready" != "true" ]]; then
    echo "RoadRunner did not become ready." >&2
    cat "$LOG" >&2
    exit 1
fi

if [[ "$(<"$BODY")" != "octane-worker" ]]; then
    echo "Unexpected Octane worker response: $(<"$BODY")" >&2
    exit 1
fi

internal_status="$(curl -sS --max-time 5 -o /dev/null -w '%{http_code}' \
    -X POST "$BASE_URL/_internal/gateway/plan")"
if [[ "$internal_status" != "404" ]]; then
    echo "Internal planner route leaked publicly: HTTP $internal_status" >&2
    exit 1
fi

curl -fsS --max-time 5 \
    -X POST \
    -H 'Content-Type: application/json' \
    --data '{"event":"created"}' \
    "$BASE_URL/octane-smoke/pass-through" >"$BODY"
if [[ "$(<"$BODY")" != 'octane-pass-through:{"event":"created"}' ]]; then
    echo "Unexpected pass-through response: $(<"$BODY")" >&2
    exit 1
fi

# The RoadRunner Router does not plan public paths itself: they reach Laravel directly, as
# the pass-through above shows. HTTP planning belongs to an installable traffic extension
# mounted under /hooks, so with none installed that edge must fail closed instead of falling
# through to PHP. scripts/http-smoke.sh covers a planned HTTP response.
hooks_status="$(curl -sS --max-time 5 -o "$BODY" -w '%{http_code}' "$BASE_URL/hooks/octane-smoke/plan")"
if [[ "$hooks_status" != "503" ]]; then
    echo "Unexpected status from the extension edge with no extension installed: $hooks_status" >&2
    exit 1
fi

if [[ "$(<"$BODY")" != *"Gateway extension for this route is not installed"* ]]; then
    echo "Unexpected extension edge body: $(<"$BODY")" >&2
    exit 1
fi

echo "Octane/RoadRunner integration smoke passed."
