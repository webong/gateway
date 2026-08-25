#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_PATH="${WEB_RELAY_HTTP_APP_PATH:-$ROOT/tests/Fixtures/octane-app}"
PHP_PORT="${WEB_RELAY_HTTP_PHP_PORT:-18081}"
RELAY_PORT="${WEB_RELAY_HTTP_RELAY_PORT:-18082}"
TOKEN="${WEB_RELAY_INTERNAL_TOKEN:-http-smoke-token}"
LOG_LEVEL="${WEB_RELAY_HTTP_LOG_LEVEL:-error}"
PHP_URL="http://127.0.0.1:$PHP_PORT"
RELAY_URL="http://127.0.0.1:$RELAY_PORT"

TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/web-relay-http.XXXXXX")"
PHP_LOG="$TEMP_DIR/php.log"
RELAY_LOG="$TEMP_DIR/relay.log"
HEADERS="$TEMP_DIR/response.headers"
BODY="$TEMP_DIR/response.body"
RECEIVER_FILE="$TEMP_DIR/receiver.txt"
RELAY_BIN="$TEMP_DIR/web-relay"
PHP_PID=""
RELAY_PID=""

cleanup() {
    if [[ -n "$RELAY_PID" ]] && kill -0 "$RELAY_PID" 2>/dev/null; then
        kill "$RELAY_PID" 2>/dev/null || true
        wait "$RELAY_PID" 2>/dev/null || true
    fi

    if [[ -n "$PHP_PID" ]] && kill -0 "$PHP_PID" 2>/dev/null; then
        kill "$PHP_PID" 2>/dev/null || true
        wait "$PHP_PID" 2>/dev/null || true
    fi

    rm -f "$PHP_LOG" "$RELAY_LOG" "$HEADERS" "$BODY" "$RECEIVER_FILE" "$RELAY_BIN"
    rmdir "$TEMP_DIR" 2>/dev/null || true
}

trap cleanup EXIT INT TERM

if [[ ! -f "$APP_PATH/bootstrap/app.php" || ! -f "$APP_PATH/public/index.php" ]]; then
    echo "Laravel HTTP fixture is incomplete: $APP_PATH" >&2
    exit 1
fi

if [[ ! -f "$ROOT/vendor/autoload.php" ]]; then
    echo "Composer dependencies not found: $ROOT/vendor/autoload.php" >&2
    echo "Install the Composer dependencies before running this smoke test." >&2
    exit 1
fi

(
    cd "$APP_PATH"
    APP_ENV=testing \
    APP_KEY="base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" \
    COMPOSER_VENDOR_DIR="$ROOT/vendor" \
    WEB_RELAY_INTERNAL_TOKEN="$TOKEN" \
    WEB_RELAY_HTTP_RECEIVER_URL="http://127.0.0.1:$PHP_PORT/http-smoke/receiver" \
    WEB_RELAY_HTTP_RECEIVER_FILE="$RECEIVER_FILE" \
    WEB_RELAY_PLANNER='Webong\WebRelay\Tests\Fixtures\HttpSmokePlanner' \
    php -S "127.0.0.1:$PHP_PORT" -t public public/index.php
) >"$PHP_LOG" 2>&1 &
PHP_PID=$!

php_ready="false"
for _ in $(seq 1 60); do
    if curl -fsS --max-time 1 "$PHP_URL/http-smoke/worker" >"$BODY" 2>/dev/null; then
        php_ready="true"
        break
    fi

    if ! kill -0 "$PHP_PID" 2>/dev/null; then
        cat "$PHP_LOG" >&2
        exit 1
    fi

    sleep 0.25
done

if [[ "$php_ready" != "true" ]]; then
    echo "Laravel HTTP backend did not become ready." >&2
    cat "$PHP_LOG" >&2
    exit 1
fi

php_internal_status="$(curl -sS --max-time 5 -o /dev/null -w '%{http_code}' \
    -X POST "$PHP_URL/_internal/web-relay/plan")"
if [[ "$php_internal_status" != "404" ]]; then
    echo "Laravel planner route accepted an unauthenticated request: HTTP $php_internal_status" >&2
    exit 1
fi

GO_BUILD_FLAGS="${GOFLAGS:--mod=mod}"
go build -tags=webrelay_smoke "$GO_BUILD_FLAGS" -o "$RELAY_BIN" "$ROOT/cmd/relayer"

WEB_RELAY_RUNTIME=http \
LARAVEL_BACKEND_URL="$PHP_URL" \
PORT="$RELAY_PORT" \
WEB_RELAY_INTERNAL_TOKEN="$TOKEN" \
LOG_LEVEL="$LOG_LEVEL" \
"$RELAY_BIN" >"$RELAY_LOG" 2>&1 &
RELAY_PID=$!

relay_ready="false"
for _ in $(seq 1 60); do
    if curl -fsS --max-time 1 "$RELAY_URL/http-smoke/worker" >"$BODY" 2>/dev/null; then
        relay_ready="true"
        break
    fi

    if ! kill -0 "$RELAY_PID" 2>/dev/null; then
        cat "$RELAY_LOG" >&2
        exit 1
    fi

    sleep 0.25
done

if [[ "$relay_ready" != "true" ]]; then
    echo "Go HTTP relay did not become ready." >&2
    cat "$RELAY_LOG" >&2
    exit 1
fi

if [[ "$(<"$BODY")" != "http-worker" ]]; then
    echo "Unexpected Go-to-Laravel pass-through response: $(<"$BODY")" >&2
    exit 1
fi

internal_status="$(curl -sS --max-time 5 -o /dev/null -w '%{http_code}' \
    -X POST "$RELAY_URL/_internal/web-relay/plan")"
if [[ "$internal_status" != "404" ]]; then
    echo "Internal planner route leaked through the Go edge: HTTP $internal_status" >&2
    exit 1
fi

curl -fsS --max-time 5 \
    -X POST \
    -H 'Content-Type: application/json' \
    --data '{"event":"created"}' \
    "$RELAY_URL/http-smoke/pass-through" >"$BODY"
if [[ "$(<"$BODY")" != '{"event":"created"}' ]]; then
    echo "Unexpected HTTP pass-through response: $(<"$BODY")" >&2
    exit 1
fi

curl -sS --max-time 5 -D "$HEADERS" -o "$BODY" "$RELAY_URL/http-smoke/plan"
plan_status="$(awk '/^HTTP\// { status = $2 } END { print status }' "$HEADERS")"
plan_header="$(awk 'tolower($1) == "x-web-relay-plan:" { sub(/\r$/, "", $2); print $2; exit }' "$HEADERS")"

if [[ "$plan_status" != "202" || "$plan_header" != "http" || "$(<"$BODY")" != "planned-by-http" ]]; then
    echo "Unexpected HTTP planner response: status=$plan_status header=$plan_header body=$(<"$BODY")" >&2
    exit 1
fi

curl -sS --max-time 5 \
    -X POST \
    -H 'Content-Type: application/json' \
    --data '{"event":"delivered"}' \
    "$RELAY_URL/http-smoke/relay" >"$BODY"
if [[ "$(<"$BODY")" != "Request accepted" ]]; then
    echo "Unexpected relay response: $(<"$BODY")" >&2
    exit 1
fi

received="false"
for _ in $(seq 1 60); do
    if [[ -s "$RECEIVER_FILE" ]] && [[ "$(wc -l <"$RECEIVER_FILE")" -ge 2 ]]; then
        received="true"
        break
    fi

    if ! kill -0 "$RELAY_PID" 2>/dev/null; then
        cat "$RELAY_LOG" >&2
        exit 1
    fi

    sleep 0.25
done

if [[ "$received" != "true" ]]; then
    echo "Subscriber receiver did not receive the relay." >&2
    cat "$RELAY_LOG" >&2
    exit 1
fi

receiver_body="$(sed -n '1p' "$RECEIVER_FILE")"
receiver_delivery_id="$(sed -n '2p' "$RECEIVER_FILE")"
if [[ "$receiver_body" != '{"event":"delivered"}' ]] || ! [[ "$receiver_delivery_id" =~ ^[a-f0-9]{64}$ ]]; then
    echo "Unexpected subscriber delivery: body=$receiver_body delivery_id=$receiver_delivery_id" >&2
    exit 1
fi

echo "HTTP runtime integration smoke passed."
