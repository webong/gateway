#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_PATH="${GATEWAY_WEBSOCKET_APP_PATH:-$ROOT/tests/Fixtures/octane-app}"
PHP_PORT="${GATEWAY_WEBSOCKET_PHP_PORT:-18281}"
HTTP_PORT="${GATEWAY_WEBSOCKET_HTTP_PORT:-18282}"
RR_PORT="${GATEWAY_WEBSOCKET_RR_PORT:-18283}"
TOKEN="${GATEWAY_INTERNAL_TOKEN:-websocket-smoke-token}"

TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/gateway-websocket.XXXXXX")"
PHP_LOG="$TEMP_DIR/php.log"
HTTP_LOG="$TEMP_DIR/http.log"
RR_LOG="$TEMP_DIR/rr.log"
RECEIVER_FILE="$TEMP_DIR/receiver.txt"
DATABASE_FILE="$TEMP_DIR/registry.sqlite"
RR_CONFIG="$TEMP_DIR/rr.yaml"
RELAY_BIN="$TEMP_DIR/gateway"
CLIENT_BIN="$TEMP_DIR/websocket-smoke"
PHP_PID=""
HTTP_PID=""
RR_PID=""

cleanup() {
	local status=$?
	for pid in "$HTTP_PID" "$RR_PID" "$PHP_PID"; do
		if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
			kill "$pid" 2>/dev/null || true
			wait "$pid" 2>/dev/null || true
		fi
	done

	if [[ "$status" -ne 0 ]]; then
		echo "WebSocket smoke diagnostics preserved at $TEMP_DIR" >&2
		return
	fi

	rm -f "$PHP_LOG" "$HTTP_LOG" "$RR_LOG" "$RECEIVER_FILE" "$DATABASE_FILE" \
		"$RR_CONFIG" "$RELAY_BIN" "$CLIENT_BIN"
	rmdir "$TEMP_DIR" 2>/dev/null || true
}

trap cleanup EXIT INT TERM

if [[ ! -f "$APP_PATH/bootstrap/app.php" || ! -f "$APP_PATH/public/index.php" ]]; then
	echo "Laravel WebSocket fixture is incomplete: $APP_PATH" >&2
	exit 1
fi
if [[ ! -x "$ROOT/vendor/bin/roadrunner-worker" || ! -f "$ROOT/vendor/autoload.php" ]]; then
	echo "Composer dependencies are required for the WebSocket smoke test." >&2
	exit 1
fi

touch "$DATABASE_FILE"
GO_BUILD_FLAGS="${GOFLAGS:--mod=mod}"
go build -tags=gateway_smoke "$GO_BUILD_FLAGS" -o "$RELAY_BIN" "$ROOT/cmd/proxy"
go build "$GO_BUILD_FLAGS" -o "$CLIENT_BIN" "$ROOT/cmd/websocket-smoke"

(
	cd "$APP_PATH"
	APP_ENV=testing \
	APP_KEY="base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" \
	COMPOSER_VENDOR_DIR="$ROOT/vendor" \
	GATEWAY_INTERNAL_TOKEN="$TOKEN" \
	GATEWAY_PROTOCOL_PLANNER='Webong\Gateway\Tests\Fixtures\WebSocketSmokeProtocolPlanner' \
	GATEWAY_WS_RECEIVER_URL="http://127.0.0.1:$PHP_PORT/http-smoke/receiver" \
	GATEWAY_HTTP_RECEIVER_FILE="$RECEIVER_FILE" \
	DB_CONNECTION=sqlite \
	DB_DATABASE="$DATABASE_FILE" \
	exec php -S "127.0.0.1:$PHP_PORT" -t public public/index.php
) >"$PHP_LOG" 2>&1 &
PHP_PID=$!

wait_http() {
	local url="$1"
	local log="$2"
	local pid="$3"
	for _ in $(seq 1 60); do
		if curl -fsS --max-time 1 "$url" >/dev/null 2>&1; then
			return 0
		fi
		if ! kill -0 "$pid" 2>/dev/null; then
			cat "$log" >&2
			return 1
		fi
		sleep 0.25
	done
	cat "$log" >&2
	return 1
}

wait_receiver() {
	for _ in $(seq 1 60); do
		if [[ -s "$RECEIVER_FILE" ]] && grep -q 'message from WebSocket' "$RECEIVER_FILE"; then
			return 0
		fi
		sleep 0.25
	done
	echo "WebSocket subscriber receiver did not receive the message." >&2
	return 1
}

wait_http "http://127.0.0.1:$PHP_PORT/http-smoke/worker" "$PHP_LOG" "$PHP_PID"

run_http_mode() {
	: >"$RECEIVER_FILE"
	GATEWAY_RUNTIME=http \
	GATEWAY_LARAVEL_BACKEND_URL="http://127.0.0.1:$PHP_PORT" \
	PORT="$HTTP_PORT" \
	GATEWAY_INTERNAL_TOKEN="$TOKEN" \
	LOG_LEVEL=error \
	"$RELAY_BIN" >"$HTTP_LOG" 2>&1 &
	HTTP_PID=$!

	wait_http "http://127.0.0.1:$HTTP_PORT/health" "$HTTP_LOG" "$HTTP_PID"
	"$CLIENT_BIN" -addr "127.0.0.1:$HTTP_PORT"
	wait_receiver

	kill "$HTTP_PID" 2>/dev/null || true
	wait "$HTTP_PID" 2>/dev/null || true
	HTTP_PID=""
	echo "WebSocket HTTP-runtime integration smoke passed."
}

cat >"$RR_CONFIG" <<EOF
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
    GATEWAY_PLANNER: 'Webong\\Gateway\\Tests\\Fixtures\\OctaneSmokePlanner'
    GATEWAY_PROTOCOL_PLANNER: 'Webong\\Gateway\\Tests\\Fixtures\\WebSocketSmokeProtocolPlanner'
    GATEWAY_WS_RECEIVER_URL: "http://127.0.0.1:$PHP_PORT/http-smoke/receiver"
    GATEWAY_HTTP_RECEIVER_FILE: "$RECEIVER_FILE"
    DB_CONNECTION: "sqlite"
    DB_DATABASE: "$DATABASE_FILE"

http:
  address: "127.0.0.1:$RR_PORT"
  middleware: ["gateway", "gzip"]
  max_request_size: 10
  pool:
    num_workers: 1

logs:
  level: "error"
EOF

run_roadrunner_mode() {
	: >"$RECEIVER_FILE"
	GATEWAY_RUNTIME=roadrunner \
	ROADRUNNER_CONFIG="$RR_CONFIG" \
	GATEWAY_INTERNAL_TOKEN="$TOKEN" \
	LOG_LEVEL=error \
	"$RELAY_BIN" >"$RR_LOG" 2>&1 &
	RR_PID=$!

	wait_http "http://127.0.0.1:$RR_PORT/octane-smoke/worker" "$RR_LOG" "$RR_PID"
	"$CLIENT_BIN" -addr "127.0.0.1:$RR_PORT"
	wait_receiver

	kill "$RR_PID" 2>/dev/null || true
	wait "$RR_PID" 2>/dev/null || true
	RR_PID=""
	echo "WebSocket RoadRunner integration smoke passed."
}

run_http_mode
run_roadrunner_mode
echo "WebSocket runtime integration smoke passed."
