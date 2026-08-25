#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_PATH="${NET_GATEWAY_OCTANE_APP_PATH:-$ROOT/tests/Fixtures/octane-app}"
PORT="${NET_GATEWAY_OCTANE_PORT:-18080}"
TOKEN="${NET_GATEWAY_INTERNAL_TOKEN:-octane-smoke-token}"
LOG_LEVEL="${NET_GATEWAY_OCTANE_LOG_LEVEL:-error}"
BASE_URL="http://127.0.0.1:$PORT"

TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/net-gateway-octane.XXXXXX")"
CONFIG="$TEMP_DIR/config.yaml"
LOG="$TEMP_DIR/relay.log"
HEADERS="$TEMP_DIR/response.headers"
BODY="$TEMP_DIR/response.body"
RELAY_BIN="$TEMP_DIR/net-gateway"
RELAY_PID=""

cleanup() {
    if [[ -n "$RELAY_PID" ]] && kill -0 "$RELAY_PID" 2>/dev/null; then
        kill "$RELAY_PID" 2>/dev/null || true
        wait "$RELAY_PID" 2>/dev/null || true
    fi

    rm -f "$CONFIG" "$LOG" "$HEADERS" "$BODY" "$RELAY_BIN"
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
    NET_GATEWAY_INTERNAL_TOKEN: "$TOKEN"
    NET_GATEWAY_PLANNER: 'Webong\NetGateway\Tests\Fixtures\OctaneSmokePlanner'

http:
  address: "127.0.0.1:$PORT"
  middleware: ["net_gateway", "gzip"]
  max_request_size: 10
  pool:
    num_workers: 1

logs:
  level: "$LOG_LEVEL"
EOF

GO_BUILD_FLAGS="${GOFLAGS:--mod=mod}"
go build "$GO_BUILD_FLAGS" -o "$RELAY_BIN" "$ROOT/cmd/relayer"

NET_GATEWAY_RUNTIME=roadrunner \
ROADRUNNER_CONFIG="$CONFIG" \
NET_GATEWAY_INTERNAL_TOKEN="$TOKEN" \
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
    -X POST "$BASE_URL/_internal/net-gateway/plan")"
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

curl -sS --max-time 5 -D "$HEADERS" -o "$BODY" "$BASE_URL/octane-smoke/plan"
plan_status="$(awk '/^HTTP\// { status = $2 } END { print status }' "$HEADERS")"
plan_header="$(awk 'tolower($1) == "x-net-gateway-plan:" { sub(/\r$/, "", $2); print $2; exit }' "$HEADERS")"

if [[ "$plan_status" != "202" ]]; then
    echo "Unexpected planned response status: $plan_status" >&2
    exit 1
fi

if [[ "$plan_header" != "octane" ]]; then
    echo "Unexpected planner response header: $plan_header" >&2
    exit 1
fi

if [[ "$(<"$BODY")" != "planned-by-octane" ]]; then
    echo "Unexpected planner response body: $(<"$BODY")" >&2
    exit 1
fi

echo "Octane/RoadRunner integration smoke passed."
