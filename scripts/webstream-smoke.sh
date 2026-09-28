#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_PATH="${GATEWAY_WEBSTREAM_APP_PATH:-$ROOT/tests/Fixtures/octane-app}"
PHP_PORT="${GATEWAY_WEBSTREAM_PHP_PORT:-18284}"
HTTP_PORT="${GATEWAY_WEBSTREAM_HTTP_PORT:-18285}"
TOKEN="${GATEWAY_INTERNAL_TOKEN:-webstream-smoke-token}"
TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/gateway-webstream.XXXXXX")"
PHP_LOG="$TEMP_DIR/php.log"
HTTP_LOG="$TEMP_DIR/http.log"
BODY="$TEMP_DIR/body.txt"
RELAY_BIN="$TEMP_DIR/gateway"
PHP_PID=""
HTTP_PID=""

cleanup() {
	local status=$?
	for pid in "$HTTP_PID" "$PHP_PID"; do
		if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; fi
	done
	if [[ "$status" -ne 0 ]]; then echo "Webstream smoke diagnostics preserved at $TEMP_DIR" >&2; return; fi
	rm -f "$PHP_LOG" "$HTTP_LOG" "$BODY" "$RELAY_BIN"
	rmdir "$TEMP_DIR" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

if [[ ! -f "$APP_PATH/bootstrap/app.php" || ! -f "$ROOT/vendor/autoload.php" ]]; then
	echo "Laravel Webstream fixture or Composer dependencies are incomplete." >&2
	exit 1
fi

GO_BUILD_FLAGS="${GOFLAGS:--mod=mod}"
go build -tags=gateway_smoke "$GO_BUILD_FLAGS" -o "$RELAY_BIN" "$ROOT/src/spinner/cmd/proxy"
(
	cd "$APP_PATH"
	APP_ENV=testing APP_KEY="base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" COMPOSER_VENDOR_DIR="$ROOT/vendor" \
	GATEWAY_INTERNAL_TOKEN="$TOKEN" GATEWAY_PROTOCOL_PLANNER='Webong\Gateway\Tests\Fixtures\WebstreamSmokeProtocolPlanner' \
	GATEWAY_WEBSTREAM_SOURCE_URL="http://127.0.0.1:$PHP_PORT/http-smoke/stream" \
	exec php -S "127.0.0.1:$PHP_PORT" -t public public/index.php
) >"$PHP_LOG" 2>&1 &
PHP_PID=$!

wait_http() {
	local url="$1" pid="$2" log="$3"
	for _ in $(seq 1 60); do
		if curl -fsS --max-time 1 "$url" >/dev/null 2>&1; then return; fi
		if ! kill -0 "$pid" 2>/dev/null; then cat "$log" >&2; return 1; fi
		sleep 0.25
	done
	cat "$log" >&2; return 1
}

wait_http "http://127.0.0.1:$PHP_PORT/http-smoke/worker" "$PHP_PID" "$PHP_LOG"
GATEWAY_RUNTIME=http GATEWAY_LARAVEL_BACKEND_URL="http://127.0.0.1:$PHP_PORT" PORT="$HTTP_PORT" GATEWAY_INTERNAL_TOKEN="$TOKEN" LOG_LEVEL=error \
"$RELAY_BIN" >"$HTTP_LOG" 2>&1 &
HTTP_PID=$!
wait_http "http://127.0.0.1:$HTTP_PORT/health" "$HTTP_PID" "$HTTP_LOG"

curl -fsSN --max-time 5 -H 'Accept: text/event-stream' -H 'Last-Event-ID: event-42' "http://127.0.0.1:$HTTP_PORT/hooks/updates" >"$BODY"
grep -Fx 'retry: 3000' "$BODY" >/dev/null
grep -Fx 'id: event-43' "$BODY" >/dev/null
grep -Fx 'data: resumed=event-42' "$BODY" >/dev/null
metrics="$(curl -fsS "http://127.0.0.1:$HTTP_PORT/metrics")"
[[ "$metrics" == *'"webstreams_opened_total":1'* ]]
echo "Webstream HTTP-runtime integration smoke passed."
