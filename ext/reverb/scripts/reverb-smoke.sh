#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
APP_PATH="${GATEWAY_REVERB_APP_PATH:-$ROOT/tests/Fixtures/octane-app}"
PHP_PORT="${GATEWAY_REVERB_PHP_PORT:-18381}"
GATEWAY_PORT="${GATEWAY_REVERB_GATEWAY_PORT:-18382}"
TOKEN="${GATEWAY_INTERNAL_TOKEN:-reverb-smoke-internal-token}"
REGISTRY_TOKEN="${REGISTRY_TOKEN:-reverb-smoke-registry-token}"
SMOKE_PROVIDERS='Laravel\Reverb\ApplicationManagerServiceProvider,Laravel\Reverb\ReverbServiceProvider,Webong\Gateway\Reverb\ReverbExtensionServiceProvider,Webong\Gateway\Reverb\Tests\Fixtures\ReverbSmokeServiceProvider'
PHP_BIN="${GATEWAY_REVERB_PHP_BINARY:-$(command -v php)}"
PHP_URL="http://127.0.0.1:$PHP_PORT"
GATEWAY_URL="http://127.0.0.1:$GATEWAY_PORT"

TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/gateway-reverb.XXXXXX")"
PHP_LOG="$TEMP_DIR/php.log"
GATEWAY_LOG="$TEMP_DIR/gateway.log"
DATABASE_FILE="$TEMP_DIR/reverb.sqlite"
BODY="$TEMP_DIR/body.json"
GATEWAY_BIN="$TEMP_DIR/gateway"
CLIENT_BIN="$TEMP_DIR/reverb-smoke"
PHP_PID=""
GATEWAY_PID=""

cleanup() {
    local status=$?
    for pid in "$GATEWAY_PID" "$PHP_PID"; do
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
        fi
    done

    if [[ "$status" -ne 0 ]]; then
        echo "Reverb smoke diagnostics preserved at $TEMP_DIR" >&2
        return
    fi

    rm -f "$PHP_LOG" "$GATEWAY_LOG" "$DATABASE_FILE" "$BODY" "$GATEWAY_BIN" "$CLIENT_BIN"
    rmdir "$TEMP_DIR" 2>/dev/null || true
}

trap cleanup EXIT INT TERM

if [[ ! -f "$APP_PATH/artisan" || ! -f "$ROOT/vendor/autoload.php" ]]; then
    echo "Laravel fixture and Composer dependencies are required for the Reverb smoke test." >&2
    exit 1
fi

touch "$DATABASE_FILE"
GO_BUILD_FLAGS="${GOFLAGS:--mod=mod}"
go build "$GO_BUILD_FLAGS" -o "$GATEWAY_BIN" "$ROOT/src/spinner/cmd/proxy"
go build "$GO_BUILD_FLAGS" -o "$CLIENT_BIN" "$ROOT/ext/reverb/cmd/reverb-smoke"

(
    cd "$APP_PATH"
    APP_ENV=testing \
    APP_KEY="base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" \
    COMPOSER_VENDOR_DIR="$ROOT/vendor" \
    GATEWAY_INTERNAL_TOKEN="$TOKEN" \
    GATEWAY_PLANNER='Webong\Gateway\Tests\Fixtures\HttpSmokePlanner' \
    GATEWAY_PATH_RESOLVER='Webong\Gateway\Tests\Fixtures\HttpSmokePathResolver' \
    GATEWAY_HTTP_REGISTRY_SMOKE=1 \
    GATEWAY_REVERB_SMOKE=1 \
    GATEWAY_SMOKE_PROVIDERS="$SMOKE_PROVIDERS" \
    REGISTRY_TOKEN="$REGISTRY_TOKEN" \
    DB_CONNECTION=sqlite \
    DB_DATABASE="$DATABASE_FILE" \
    exec "$PHP_BIN" -S "127.0.0.1:$PHP_PORT" -t public public/index.php
) >"$PHP_LOG" 2>&1 &
PHP_PID=$!

for _ in $(seq 1 60); do
    if curl -fsS --max-time 1 "$PHP_URL/http-smoke/worker" >/dev/null 2>&1; then
        break
    fi
    if ! kill -0 "$PHP_PID" 2>/dev/null; then
        cat "$PHP_LOG" >&2
        exit 1
    fi
    sleep 0.25
done

curl -fsS --max-time 5 -o "$BODY" \
    -X POST \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    -H 'Content-Type: application/json' \
    --data '{"type":"reverb","name":"reverb-smoke","hostname":"reverb.example.test","path":"/reverb","replicas":2,"configuration":{"scaling":{"enabled":false}}}' \
    "$PHP_URL/servers"
SERVER_ID="$(php -r '$value=json_decode(file_get_contents($argv[1]), true, 512, JSON_THROW_ON_ERROR); echo $value["id"] ?? "";' "$BODY")"
if [[ -z "$SERVER_ID" ]]; then
    echo "Reverb server creation failed: $(<"$BODY")" >&2
    exit 1
fi

curl -fsS --max-time 5 \
    -X POST \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    -H 'Content-Type: application/json' \
    --data "{\"server_id\":\"$SERVER_ID\",\"app_id\":\"reverb-smoke-app\",\"key\":\"reverb-smoke-key\",\"secret\":\"reverb-smoke-private-secret\",\"allowed_origins\":[\"client.example.test\"]}" \
    "$PHP_URL/applications" >"$BODY"

curl -fsS --max-time 5 -o "$BODY" \
    -X POST \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    -H 'Content-Type: application/json' \
    --data '{"type":"reverb","name":"reverb-secondary","hostname":"secondary-reverb.example.test","path":"/secondary","replicas":1,"configuration":{"scaling":{"enabled":false}}}' \
    "$PHP_URL/servers"
SECOND_SERVER_ID="$(php -r '$value=json_decode(file_get_contents($argv[1]), true, 512, JSON_THROW_ON_ERROR); echo $value["id"] ?? "";' "$BODY")"
if [[ -z "$SECOND_SERVER_ID" ]]; then
    echo "Secondary Reverb server creation failed: $(<"$BODY")" >&2
    exit 1
fi

curl -fsS --max-time 5 \
    -X POST \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    -H 'Content-Type: application/json' \
    --data "{\"server_id\":\"$SECOND_SERVER_ID\",\"app_id\":\"secondary-reverb-app\",\"key\":\"secondary-reverb-key\",\"secret\":\"secondary-reverb-private-secret\",\"allowed_origins\":[\"client.example.test\"]}" \
    "$PHP_URL/applications" >"$BODY"

GATEWAY_RUNTIME=http \
GATEWAY_LARAVEL_BACKEND_URL="$PHP_URL" \
PORT="$GATEWAY_PORT" \
GATEWAY_INTERNAL_TOKEN="$TOKEN" \
GATEWAY_PLANNER='Webong\Gateway\Tests\Fixtures\HttpSmokePlanner' \
GATEWAY_PATH_RESOLVER='Webong\Gateway\Tests\Fixtures\HttpSmokePathResolver' \
GATEWAY_PROVISIONING_ENABLED=true \
GATEWAY_PROVISIONING_POLL_INTERVAL=250ms \
GATEWAY_REVERB_ENABLED=true \
GATEWAY_REVERB_PHP_BINARY="$PHP_BIN" \
GATEWAY_REVERB_WORKING_DIRECTORY="$APP_PATH" \
COMPOSER_VENDOR_DIR="$ROOT/vendor" \
APP_ENV=testing \
APP_KEY="base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" \
GATEWAY_HTTP_REGISTRY_SMOKE=1 \
GATEWAY_REVERB_SMOKE=1 \
GATEWAY_SMOKE_PROVIDERS="$SMOKE_PROVIDERS" \
GATEWAY_SMOKE_SKIP_MIGRATIONS=1 \
REGISTRY_TOKEN="$REGISTRY_TOKEN" \
DB_CONNECTION=sqlite \
DB_DATABASE="$DATABASE_FILE" \
LOG_LEVEL=error \
"$GATEWAY_BIN" >"$GATEWAY_LOG" 2>&1 &
GATEWAY_PID=$!

for _ in $(seq 1 60); do
    if curl -fsS --max-time 1 "$GATEWAY_URL/health" >/dev/null 2>&1; then
        break
    fi
    if ! kill -0 "$GATEWAY_PID" 2>/dev/null; then
        cat "$GATEWAY_LOG" >&2
        exit 1
    fi
    sleep 0.25
done

curl -fsS --max-time 5 \
    -X POST \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    "$GATEWAY_URL/servers/$SERVER_ID/start" >"$BODY"
curl -fsS --max-time 5 \
    -X POST \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    "$GATEWAY_URL/servers/$SECOND_SERVER_ID/start" >"$BODY"

running="false"
for _ in $(seq 1 80); do
    count="$(curl -fsS --max-time 2 -H "Authorization: Bearer $REGISTRY_TOKEN" \
        "$GATEWAY_URL/instances?server_id=$SERVER_ID" \
        | php -r '$value=json_decode(stream_get_contents(STDIN), true, 512, JSON_THROW_ON_ERROR); echo count(array_filter($value["data"] ?? [], fn ($item) => ($item["state"] ?? null) === "running"));')"
    if [[ "$count" == "2" ]]; then
        running="true"
        break
    fi
    sleep 0.25
done
if [[ "$running" != "true" ]]; then
    echo "Gateway did not reconcile two Reverb instances." >&2
    cat "$PHP_LOG" "$GATEWAY_LOG" >&2
    exit 1
fi

secondary_running="false"
for _ in $(seq 1 80); do
    count="$(curl -fsS --max-time 2 -H "Authorization: Bearer $REGISTRY_TOKEN" \
        "$GATEWAY_URL/instances?server_id=$SECOND_SERVER_ID" \
        | php -r '$value=json_decode(stream_get_contents(STDIN), true, 512, JSON_THROW_ON_ERROR); echo count(array_filter($value["data"] ?? [], fn ($item) => ($item["state"] ?? null) === "running"));')"
    if [[ "$count" == "1" ]]; then
        secondary_running="true"
        break
    fi
    sleep 0.25
done
if [[ "$secondary_running" != "true" ]]; then
    echo "Gateway did not reconcile the secondary Reverb server." >&2
    cat "$PHP_LOG" "$GATEWAY_LOG" >&2
    exit 1
fi

"$CLIENT_BIN" -addr "127.0.0.1:$GATEWAY_PORT"
"$CLIENT_BIN" \
    -addr "127.0.0.1:$GATEWAY_PORT" \
    -hostname "secondary-reverb.example.test" \
    -path "/secondary/app/secondary-reverb-key"

curl -fsS --max-time 5 \
    -X POST \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    "$GATEWAY_URL/servers/$SERVER_ID/stop" >"$BODY"
curl -fsS --max-time 5 \
    -X POST \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    "$GATEWAY_URL/servers/$SECOND_SERVER_ID/stop" >"$BODY"

stopped="false"
for _ in $(seq 1 80); do
    count="$(curl -fsS --max-time 2 -H "Authorization: Bearer $REGISTRY_TOKEN" \
        "$GATEWAY_URL/instances?server_id=$SERVER_ID" \
        | php -r '$value=json_decode(stream_get_contents(STDIN), true, 512, JSON_THROW_ON_ERROR); echo count(array_filter($value["data"] ?? [], fn ($item) => in_array(($item["state"] ?? null), ["starting", "running"], true)));')"
    if [[ "$count" == "0" ]]; then
        stopped="true"
        break
    fi
    sleep 0.25
done
if [[ "$stopped" != "true" ]]; then
    echo "Gateway did not stop the managed Reverb instances." >&2
    cat "$GATEWAY_LOG" >&2
    exit 1
fi

secondary_stopped="false"
for _ in $(seq 1 80); do
    count="$(curl -fsS --max-time 2 -H "Authorization: Bearer $REGISTRY_TOKEN" \
        "$GATEWAY_URL/instances?server_id=$SECOND_SERVER_ID" \
        | php -r '$value=json_decode(stream_get_contents(STDIN), true, 512, JSON_THROW_ON_ERROR); echo count(array_filter($value["data"] ?? [], fn ($item) => in_array(($item["state"] ?? null), ["starting", "running"], true)));')"
    if [[ "$count" == "0" ]]; then
        secondary_stopped="true"
        break
    fi
    sleep 0.25
done
if [[ "$secondary_stopped" != "true" ]]; then
    echo "Gateway did not stop the secondary Reverb server." >&2
    cat "$GATEWAY_LOG" >&2
    exit 1
fi

echo "Multi-server Reverb management integration smoke passed."
