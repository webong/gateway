#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_PATH="${GATEWAY_HTTP_APP_PATH:-$ROOT/tests/Fixtures/octane-app}"
PHP_PORT="${GATEWAY_HTTP_PHP_PORT:-18081}"
RELAY_PORT="${GATEWAY_HTTP_RELAY_PORT:-18082}"
TOKEN="${GATEWAY_INTERNAL_TOKEN:-http-smoke-token}"
REGISTRY_TOKEN="${REGISTRY_TOKEN:-registry-smoke-token}"
LOG_LEVEL="${GATEWAY_HTTP_LOG_LEVEL:-error}"
PHP_URL="http://127.0.0.1:$PHP_PORT"
RELAY_URL="http://127.0.0.1:$RELAY_PORT"

TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/gateway-http.XXXXXX")"
PHP_LOG="$TEMP_DIR/php.log"
RELAY_LOG="$TEMP_DIR/relay.log"
HEADERS="$TEMP_DIR/response.headers"
BODY="$TEMP_DIR/response.body"
RECEIVER_FILE="$TEMP_DIR/receiver.txt"
DATABASE_FILE="$TEMP_DIR/registry.sqlite"
RELAY_BIN="$TEMP_DIR/gateway"
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

    rm -f "$PHP_LOG" "$RELAY_LOG" "$HEADERS" "$BODY" "$RECEIVER_FILE" "$DATABASE_FILE" "$RELAY_BIN"
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

touch "$DATABASE_FILE"

(
    cd "$APP_PATH"
    APP_ENV=testing \
    APP_KEY="base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" \
    COMPOSER_VENDOR_DIR="$ROOT/vendor" \
    GATEWAY_INTERNAL_TOKEN="$TOKEN" \
    GATEWAY_HTTP_RECEIVER_URL="http://127.0.0.1:$PHP_PORT/http-smoke/receiver" \
    GATEWAY_HTTP_RECEIVER_FILE="$RECEIVER_FILE" \
    GATEWAY_HTTP_REGISTRY_SMOKE=1 \
    GATEWAY_PLANNER='Webong\Gateway\Tests\Fixtures\HttpSmokePlanner' \
    GATEWAY_PATH_RESOLVER='Webong\Gateway\Tests\Fixtures\HttpSmokePathResolver' \
    REGISTRY_TOKEN="$REGISTRY_TOKEN" \
    DB_CONNECTION=sqlite \
    DB_DATABASE="$DATABASE_FILE" \
    WEB_PROXY_URL="$RELAY_URL" \
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
    -X POST "$PHP_URL/_internal/gateway/plan")"
if [[ "$php_internal_status" != "404" ]]; then
    echo "Laravel planner route accepted an unauthenticated request: HTTP $php_internal_status" >&2
    exit 1
fi

GO_BUILD_FLAGS="${GOFLAGS:--mod=mod}"
go build -tags=gateway_smoke "$GO_BUILD_FLAGS" -o "$RELAY_BIN" "$ROOT/cmd/proxy"

GATEWAY_RUNTIME=http \
GATEWAY_LARAVEL_BACKEND_URL="$PHP_URL" \
PORT="$RELAY_PORT" \
GATEWAY_INTERNAL_TOKEN="$TOKEN" \
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
    -X POST "$RELAY_URL/_internal/gateway/plan")"
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
plan_header="$(awk 'tolower($1) == "x-gateway-plan:" { sub(/\r$/, "", $2); print $2; exit }' "$HEADERS")"

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

unauthorized_registry_status="$(curl -sS --max-time 5 -o /dev/null -w '%{http_code}' \
    -X POST -H 'Content-Type: application/json' --data '{}' \
    "$RELAY_URL/registry/endpoints")"
if [[ "$unauthorized_registry_status" != "401" ]]; then
    echo "Registry API accepted an unauthenticated request: HTTP $unauthorized_registry_status" >&2
    exit 1
fi

curl -sS --max-time 5 -D "$HEADERS" -o "$BODY" \
    -X POST \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    -H 'Content-Type: application/json' \
    --data '{"client":"http-smoke","external_id":"http-smoke-provider","endpoint_key":"http-smoke-endpoint","managed":true}' \
    "$RELAY_URL/registry/endpoints"
endpoint_status="$(awk '/^HTTP\// { status = $2 } END { print status }' "$HEADERS")"
endpoint_key="$(php -r '$value=json_decode(file_get_contents($argv[1]), true, 512, JSON_THROW_ON_ERROR); echo $value["endpoint_key"] ?? "";' "$BODY")"
if [[ "$endpoint_status" != "201" || "$endpoint_key" != "http-smoke-endpoint" ]]; then
    echo "Registry endpoint creation failed: status=$endpoint_status body=$(<"$BODY")" >&2
    exit 1
fi

curl -sS --max-time 5 -D "$HEADERS" -o "$BODY" \
    -X POST \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    -H 'Content-Type: application/json' \
    --data "{\"subscriber_id\":\"http-smoke-subscriber\",\"subscription_id\":\"http-smoke-subscription\",\"webhook_group\":\"http-smoke\",\"routing_scope\":\"application\",\"routing_key\":\"app-123\",\"url\":\"$PHP_URL/http-smoke/receiver\",\"match\":{\"version\":\"v1\",\"rules\":{\"body.event\":[\"required\",\"in:registry\"]}}}" \
    "$RELAY_URL/registry/endpoints/$endpoint_key/subscriptions"
subscription_status="$(awk '/^HTTP\// { status = $2 } END { print status }' "$HEADERS")"
subscription_etag="$(awk 'tolower($1) == "etag:" { sub(/\r$/, "", $2); print $2; exit }' "$HEADERS")"
destination_id="$(php -r '$value=json_decode(file_get_contents($argv[1]), true, 512, JSON_THROW_ON_ERROR); echo $value["id"] ?? "";' "$BODY")"
if [[ "$subscription_status" != "201" || -z "$destination_id" ]] || ! [[ "$subscription_etag" =~ ^\"[a-f0-9]{32}\"$ ]]; then
    echo "Registry subscription creation failed: status=$subscription_status etag=$subscription_etag body=$(<"$BODY")" >&2
    exit 1
fi

: >"$RECEIVER_FILE"
registry_ingress_status="$(curl -sS --max-time 5 -o "$BODY" -w '%{http_code}' \
    -X POST -H 'Content-Type: application/json' --data '{"event":"registry"}' \
    "$RELAY_URL/http-smoke/registry-ingress")"
if [[ "$registry_ingress_status" != "202" ]]; then
    echo "Matching registry ingress was not accepted: HTTP $registry_ingress_status body=$(<"$BODY")" >&2
    exit 1
fi

registry_received="false"
for _ in $(seq 1 60); do
    if [[ -s "$RECEIVER_FILE" ]] && [[ "$(sed -n '1p' "$RECEIVER_FILE")" == '{"event":"registry"}' ]]; then
        registry_received="true"
        break
    fi
    sleep 0.25
done
if [[ "$registry_received" != "true" ]]; then
    echo "Matching registry subscription did not receive ingress." >&2
    exit 1
fi

curl -sS --max-time 5 -D "$HEADERS" -o "$BODY" \
    -X PATCH \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    -H "If-Match: $subscription_etag" \
    -H 'Content-Type: application/json' \
    --data '{"version":"v1","operations":[{"op":"remove","field":"body.event","rules":["in:registry"]},{"op":"add","field":"body.event","rules":["in:patched"]}]}' \
    "$RELAY_URL/registry/endpoints/$endpoint_key/subscriptions/$destination_id/match"
patch_status="$(awk '/^HTTP\// { status = $2 } END { print status }' "$HEADERS")"
patched_etag="$(awk 'tolower($1) == "etag:" { sub(/\r$/, "", $2); print $2; exit }' "$HEADERS")"
if [[ "$patch_status" != "200" || "$patched_etag" == "$subscription_etag" ]]; then
    echo "Incremental match patch failed: status=$patch_status etag=$patched_etag body=$(<"$BODY")" >&2
    exit 1
fi

stale_status="$(curl -sS --max-time 5 -o "$BODY" -w '%{http_code}' \
    -X PATCH \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    -H "If-Match: $subscription_etag" \
    -H 'Content-Type: application/json' \
    --data '{"version":"v1","operations":[{"op":"add","field":"body.stale","rules":["required"]}]}' \
    "$RELAY_URL/registry/endpoints/$endpoint_key/subscriptions/$destination_id/match")"
if [[ "$stale_status" != "412" ]]; then
    echo "Stale subscription revision was not rejected: HTTP $stale_status body=$(<"$BODY")" >&2
    exit 1
fi

: >"$RECEIVER_FILE"
old_match_status="$(curl -sS --max-time 5 -o /dev/null -w '%{http_code}' \
    -X POST -H 'Content-Type: application/json' --data '{"event":"registry"}' \
    "$RELAY_URL/http-smoke/registry-ingress")"
new_match_status="$(curl -sS --max-time 5 -o /dev/null -w '%{http_code}' \
    -X POST -H 'Content-Type: application/json' --data '{"event":"patched"}' \
    "$RELAY_URL/http-smoke/registry-ingress")"
if [[ "$old_match_status" != "204" || "$new_match_status" != "202" ]]; then
    echo "Patched registry matching failed: old=$old_match_status new=$new_match_status" >&2
    cat "$PHP_LOG" "$RELAY_LOG" >&2
    exit 1
fi

curl -sS --max-time 5 -D "$HEADERS" -o "$BODY" \
    -X PATCH \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    -H "If-Match: $patched_etag" \
    -H 'Content-Type: application/json' \
    --data '{"status":"paused"}' \
    "$RELAY_URL/registry/endpoints/$endpoint_key/subscriptions/$destination_id/status"
pause_status="$(awk '/^HTTP\// { status = $2 } END { print status }' "$HEADERS")"
paused_etag="$(awk 'tolower($1) == "etag:" { sub(/\r$/, "", $2); print $2; exit }' "$HEADERS")"
paused_ingress_status="$(curl -sS --max-time 5 -o /dev/null -w '%{http_code}' \
    -X POST -H 'Content-Type: application/json' --data '{"event":"patched"}' \
    "$RELAY_URL/http-smoke/registry-ingress")"
if [[ "$pause_status" != "200" || "$paused_ingress_status" != "204" ]]; then
    echo "Paused subscription still received ingress: patch=$pause_status ingress=$paused_ingress_status" >&2
    exit 1
fi

curl -sS --max-time 5 -D "$HEADERS" -o "$BODY" \
    -X PATCH \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    -H "If-Match: $paused_etag" \
    -H 'Content-Type: application/json' \
    --data '{"status":"active"}' \
    "$RELAY_URL/registry/endpoints/$endpoint_key/subscriptions/$destination_id/status"
active_status="$(awk '/^HTTP\// { status = $2 } END { print status }' "$HEADERS")"
active_etag="$(awk 'tolower($1) == "etag:" { sub(/\r$/, "", $2); print $2; exit }' "$HEADERS")"
if [[ "$active_status" != "200" ]]; then
    echo "Subscription reactivation failed: status=$active_status body=$(<"$BODY")" >&2
    exit 1
fi

list_count="$(curl -fsS --max-time 5 \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    "$RELAY_URL/registry/endpoints/$endpoint_key/subscriptions?routing_scope=application&routing_key=app-123" \
    | php -r '$value=json_decode(stream_get_contents(STDIN), true, 512, JSON_THROW_ON_ERROR); echo count($value["data"] ?? []);')"
if [[ "$list_count" != "1" ]]; then
    echo "Route-scoped subscription listing returned $list_count items." >&2
    exit 1
fi

benchmark_requests="${GATEWAY_HTTP_BENCHMARK_REQUESTS:-0}"
if [[ "$benchmark_requests" =~ ^[1-9][0-9]*$ ]]; then
    benchmark_concurrency="${GATEWAY_HTTP_BENCHMARK_CONCURRENCY:-20}"
    (
        cd "$ROOT"
        go run -mod=mod ./cmd/benchmark \
            -url "$RELAY_URL/http-smoke/registry-ingress" \
            -requests "$benchmark_requests" \
            -concurrency "$benchmark_concurrency" \
            -body '{"event":"non-matching-benchmark"}'
    )
fi

remove_status="$(curl -sS --max-time 5 -o /dev/null -w '%{http_code}' \
    -X DELETE \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    -H "If-Match: $active_etag" \
    "$RELAY_URL/registry/endpoints/$endpoint_key/subscriptions/$destination_id")"
if [[ "$remove_status" != "204" ]]; then
    echo "Subscription removal failed: HTTP $remove_status" >&2
    exit 1
fi

echo "HTTP runtime integration smoke passed."
