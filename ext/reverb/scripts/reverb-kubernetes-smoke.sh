#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
APP_PATH="$ROOT/tests/Fixtures/octane-app"
PHP_PORT="${GATEWAY_REVERB_PHP_PORT:-18481}"
GATEWAY_PORT="${GATEWAY_REVERB_GATEWAY_PORT:-18482}"
TOKEN="${GATEWAY_INTERNAL_TOKEN:-reverb-kubernetes-smoke-internal-token}"
REGISTRY_TOKEN="${REGISTRY_TOKEN:-reverb-kubernetes-smoke-registry-token}"
SMOKE_PROVIDERS='Laravel\Reverb\ApplicationManagerServiceProvider,Laravel\Reverb\ReverbServiceProvider,Webong\Gateway\Reverb\ReverbExtensionServiceProvider,Webong\Gateway\Reverb\Tests\Fixtures\ReverbSmokeServiceProvider'
PHP_BIN="${GATEWAY_REVERB_PHP_BINARY:-$(command -v php)}"
PHP_URL="http://127.0.0.1:$PHP_PORT"
GATEWAY_URL="http://127.0.0.1:$GATEWAY_PORT"
NAMESPACE="gateway-reverb-smoke-$$"
IMAGE="gateway-reverb-kubernetes-smoke:$$"
CONFIG_MAP="gateway-reverb-smoke"

TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/gateway-reverb-kubernetes.XXXXXX")"
PHP_LOG="$TEMP_DIR/php.log"
GATEWAY_LOG="$TEMP_DIR/gateway.log"
DATABASE_FILE="$TEMP_DIR/reverb.sqlite"
BODY="$TEMP_DIR/body.json"
ROUTING_LOG="$TEMP_DIR/routing.log"
GATEWAY_BIN="$TEMP_DIR/gateway"
CLIENT_BIN="$TEMP_DIR/reverb-smoke"
PHP_PID=""
GATEWAY_PID=""
NAMESPACE_CREATED="false"
IMAGE_CREATED="false"

cleanup() {
    local status=$?
    for pid in "$GATEWAY_PID" "$PHP_PID"; do
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
        fi
    done
    if [[ "$NAMESPACE_CREATED" == "true" ]]; then
        kubectl delete namespace "$NAMESPACE" --wait=false >/dev/null 2>&1 || true
    fi
    if [[ "$IMAGE_CREATED" == "true" ]]; then
        docker image rm --force "$IMAGE" >/dev/null 2>&1 || true
    fi

    if [[ "$status" -ne 0 ]]; then
        echo "Kubernetes Reverb smoke diagnostics preserved at $TEMP_DIR" >&2
        return
    fi

    rm -rf "$TEMP_DIR"
}

diagnostics() {
    cat "$PHP_LOG" "$GATEWAY_LOG" >&2 || true
    kubectl get pods --namespace "$NAMESPACE" -o wide >&2 || true
    kubectl describe pods --namespace "$NAMESPACE" >&2 || true
    kubectl logs --namespace "$NAMESPACE" --all-containers --prefix --selector app.kubernetes.io/name=gateway-provisioning >&2 || true
}

trap cleanup EXIT INT TERM

if [[ ! -f "$APP_PATH/artisan" || ! -f "$ROOT/vendor/autoload.php" ]]; then
    echo "Laravel fixture and Composer dependencies are required for the Kubernetes Reverb smoke test." >&2
    exit 1
fi

touch "$DATABASE_FILE"
GO_BUILD_FLAGS="${GOFLAGS:--mod=mod}"
go build "$GO_BUILD_FLAGS" -o "$GATEWAY_BIN" "$ROOT/cmd/proxy"
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
    --data '{"type":"reverb","name":"reverb-kubernetes-smoke","hostname":"reverb-kubernetes.example.test","path":"/reverb","driver":"kubernetes","replicas":2,"configuration":{"scaling":{"enabled":false}}}' \
    "$PHP_URL/servers"
PRIMARY_SERVER_ID="$(php -r '$value=json_decode(file_get_contents($argv[1]), true, 512, JSON_THROW_ON_ERROR); echo $value["id"] ?? "";' "$BODY")"
if [[ -z "$PRIMARY_SERVER_ID" ]]; then
    echo "Primary Kubernetes Reverb server creation failed: $(<"$BODY")" >&2
    exit 1
fi

curl -fsS --max-time 5 \
    -X POST \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    -H 'Content-Type: application/json' \
    --data "{\"server_id\":\"$PRIMARY_SERVER_ID\",\"app_id\":\"reverb-smoke-app\",\"key\":\"reverb-smoke-key\",\"secret\":\"reverb-smoke-private-secret\",\"allowed_origins\":[\"client.example.test\"]}" \
    "$PHP_URL/applications" >"$BODY"

curl -fsS --max-time 5 -o "$BODY" \
    -X POST \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    -H 'Content-Type: application/json' \
    --data '{"type":"reverb","name":"reverb-kubernetes-secondary","hostname":"secondary-reverb-kubernetes.example.test","path":"/secondary","driver":"kubernetes","replicas":1,"configuration":{"scaling":{"enabled":false}}}' \
    "$PHP_URL/servers"
SECONDARY_SERVER_ID="$(php -r '$value=json_decode(file_get_contents($argv[1]), true, 512, JSON_THROW_ON_ERROR); echo $value["id"] ?? "";' "$BODY")"
if [[ -z "$SECONDARY_SERVER_ID" ]]; then
    echo "Secondary Kubernetes Reverb server creation failed: $(<"$BODY")" >&2
    exit 1
fi

curl -fsS --max-time 5 \
    -X POST \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    -H 'Content-Type: application/json' \
    --data "{\"server_id\":\"$SECONDARY_SERVER_ID\",\"app_id\":\"secondary-reverb-app\",\"key\":\"secondary-reverb-key\",\"secret\":\"secondary-reverb-private-secret\",\"allowed_origins\":[\"client.example.test\"]}" \
    "$PHP_URL/applications" >"$BODY"

docker build \
    --build-context "dependencies=$ROOT/vendor" \
    --build-context "smoke=$TEMP_DIR" \
    --file "$ROOT/ext/reverb/tests/Fixtures/reverb-kubernetes.Dockerfile" \
    --tag "$IMAGE" \
    "$ROOT"
IMAGE_CREATED="true"

kubectl create namespace "$NAMESPACE" >/dev/null
NAMESPACE_CREATED="true"
kubectl create configmap "$CONFIG_MAP" \
    --namespace "$NAMESPACE" \
    --from-literal=APP_ENV=testing \
    --from-literal='APP_KEY=base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=' \
    --from-literal=COMPOSER_VENDOR_DIR=/workspace/vendor \
    --from-literal=DB_CONNECTION=sqlite \
    --from-literal=DB_DATABASE=/workspace/tests/Fixtures/octane-app/database/reverb.sqlite \
    --from-literal=GATEWAY_HTTP_REGISTRY_SMOKE=1 \
    --from-literal=GATEWAY_REVERB_SMOKE=1 \
    --from-literal=GATEWAY_SMOKE_SKIP_MIGRATIONS=1 \
    --from-literal="GATEWAY_SMOKE_PROVIDERS=$SMOKE_PROVIDERS" >/dev/null

GATEWAY_RUNTIME=http \
GATEWAY_LARAVEL_BACKEND_URL="$PHP_URL" \
PORT="$GATEWAY_PORT" \
GATEWAY_INTERNAL_TOKEN="$TOKEN" \
GATEWAY_PROVISIONING_ENABLED=true \
GATEWAY_PROVISIONING_POLL_INTERVAL=250ms \
GATEWAY_REVERB_ENABLED=true \
GATEWAY_SMOKE_PROVIDERS="$SMOKE_PROVIDERS" \
GATEWAY_SMOKE_SKIP_MIGRATIONS=1 \
GATEWAY_REVERB_KUBERNETES_IMAGE="$IMAGE" \
GATEWAY_PROVISIONING_KUBERNETES_NAMESPACE="$NAMESPACE" \
GATEWAY_PROVISIONING_KUBERNETES_ENV_CONFIG_MAP="$CONFIG_MAP" \
GATEWAY_PROVISIONING_KUBERNETES_IMAGE_PULL_POLICY=Never \
GATEWAY_PROVISIONING_KUBERNETES_ROUTE_MODE=port-forward \
GATEWAY_PROVISIONING_START_TIMEOUT=30s \
GATEWAY_PROVISIONING_STOP_TIMEOUT=15s \
GATEWAY_NODE_ID="reverb-kubernetes-smoke-$$" \
LOG_LEVEL=error \
"$GATEWAY_BIN" >"$GATEWAY_LOG" 2>&1 &
GATEWAY_PID=$!

for _ in $(seq 1 60); do
    if curl -fsS --max-time 1 "$GATEWAY_URL/health" >/dev/null 2>&1; then
        break
    fi
    if ! kill -0 "$GATEWAY_PID" 2>/dev/null; then
        diagnostics
        exit 1
    fi
    sleep 0.25
done

curl -fsS --max-time 5 \
    -X POST \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    "$GATEWAY_URL/servers/$PRIMARY_SERVER_ID/start" >"$BODY"

curl -fsS --max-time 5 \
    -X POST \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    "$GATEWAY_URL/servers/$SECONDARY_SERVER_ID/start" >"$BODY"

primary_running="0"
secondary_running="0"
for _ in $(seq 1 120); do
    primary_running="$(curl -fsS --max-time 2 -H "Authorization: Bearer $REGISTRY_TOKEN" \
        "$GATEWAY_URL/instances?server_id=$PRIMARY_SERVER_ID" \
        | php -r '$value=json_decode(stream_get_contents(STDIN), true, 512, JSON_THROW_ON_ERROR); echo count(array_filter($value["data"] ?? [], fn ($item) => ($item["state"] ?? null) === "running"));')"
    secondary_running="$(curl -fsS --max-time 2 -H "Authorization: Bearer $REGISTRY_TOKEN" \
        "$GATEWAY_URL/instances?server_id=$SECONDARY_SERVER_ID" \
        | php -r '$value=json_decode(stream_get_contents(STDIN), true, 512, JSON_THROW_ON_ERROR); echo count(array_filter($value["data"] ?? [], fn ($item) => ($item["state"] ?? null) === "running"));')"
    if [[ "$primary_running" == "2" && "$secondary_running" == "1" ]]; then
        break
    fi
    sleep 0.25
done
if [[ "$primary_running" != "2" || "$secondary_running" != "1" ]]; then
    echo "Gateway did not reconcile two primary and one secondary Kubernetes Reverb instances." >&2
    diagnostics
    exit 1
fi

kubectl wait \
    --namespace "$NAMESPACE" \
    --selector app.kubernetes.io/name=gateway-provisioning \
    --for=condition=Ready pod \
    --timeout=30s >/dev/null

pod_count="$(kubectl get pods --namespace "$NAMESPACE" --selector app.kubernetes.io/name=gateway-provisioning --no-headers | wc -l | tr -d ' ')"
if [[ "$pod_count" != "3" ]]; then
    echo "Expected three managed Kubernetes Reverb Pods, found $pod_count." >&2
    diagnostics
    exit 1
fi

primary_ports="$(curl -fsS --max-time 2 -H "Authorization: Bearer $REGISTRY_TOKEN" \
    "$GATEWAY_URL/instances?server_id=$PRIMARY_SERVER_ID" \
    | php -r '$value=json_decode(stream_get_contents(STDIN), true, 512, JSON_THROW_ON_ERROR); foreach ($value["data"] ?? [] as $item) { if (($item["state"] ?? null) === "running") { echo ($item["port"] ?? ""), PHP_EOL; } }')"
primary_port_count="$(printf '%s\n' "$primary_ports" | sed '/^$/d' | sort -u | wc -l | tr -d ' ')"
if [[ "$primary_port_count" != "2" ]]; then
    echo "Expected two distinct primary port-forward listeners, found $primary_port_count." >&2
    diagnostics
    exit 1
fi

log_mark="$(wc -c <"$GATEWAY_LOG" | tr -d ' ')"
for _ in $(seq 1 4); do
    "$CLIENT_BIN" \
        -addr "127.0.0.1:$GATEWAY_PORT" \
        -hostname "reverb-kubernetes.example.test" \
        -path "/reverb/app/reverb-smoke-key"
done

"$CLIENT_BIN" \
    -addr "127.0.0.1:$GATEWAY_PORT" \
    -hostname "secondary-reverb-kubernetes.example.test" \
    -path "/secondary/app/secondary-reverb-key"

sleep 0.5
tail -c "+$((log_mark + 1))" "$GATEWAY_LOG" >"$ROUTING_LOG"
while IFS= read -r port; do
    if [[ -n "$port" ]] && ! grep -F "Handling connection for $port" "$ROUTING_LOG" >/dev/null; then
        echo "Gateway did not route a post-readiness handshake to primary replica port $port." >&2
        diagnostics
        exit 1
    fi
done <<<"$primary_ports"

curl -fsS --max-time 5 \
    -X POST \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    "$GATEWAY_URL/servers/$PRIMARY_SERVER_ID/stop" >"$BODY"

curl -fsS --max-time 5 \
    -X POST \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    "$GATEWAY_URL/servers/$SECONDARY_SERVER_ID/stop" >"$BODY"

primary_active="1"
secondary_active="1"
for _ in $(seq 1 80); do
    primary_active="$(curl -fsS --max-time 2 -H "Authorization: Bearer $REGISTRY_TOKEN" \
        "$GATEWAY_URL/instances?server_id=$PRIMARY_SERVER_ID" \
        | php -r '$value=json_decode(stream_get_contents(STDIN), true, 512, JSON_THROW_ON_ERROR); echo count(array_filter($value["data"] ?? [], fn ($item) => in_array(($item["state"] ?? null), ["starting", "running", "stopping"], true)));')"
    secondary_active="$(curl -fsS --max-time 2 -H "Authorization: Bearer $REGISTRY_TOKEN" \
        "$GATEWAY_URL/instances?server_id=$SECONDARY_SERVER_ID" \
        | php -r '$value=json_decode(stream_get_contents(STDIN), true, 512, JSON_THROW_ON_ERROR); echo count(array_filter($value["data"] ?? [], fn ($item) => in_array(($item["state"] ?? null), ["starting", "running", "stopping"], true)));')"
    if [[ "$primary_active" == "0" && "$secondary_active" == "0" ]]; then
        break
    fi
    sleep 0.25
done

if [[ "$primary_active" != "0" || "$secondary_active" != "0" ]]; then
    echo "Kubernetes Reverb instances remained active after both servers stopped." >&2
    diagnostics
    exit 1
fi

if [[ "$(kubectl get pods --namespace "$NAMESPACE" --selector app.kubernetes.io/name=gateway-provisioning --no-headers 2>/dev/null | wc -l | tr -d ' ')" != "0" ]]; then
    echo "Kubernetes Reverb Pod remained after stop." >&2
    diagnostics
    exit 1
fi

echo "Kubernetes multi-server Reverb smoke passed (2 logical servers, 3 Pods, both primary replicas routed)."
