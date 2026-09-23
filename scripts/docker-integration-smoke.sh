#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT/docker-compose.integration.yml"
PROJECT="gateway-integration-${RANDOM}${RANDOM}"
TLS_PORT="${GATEWAY_INTEGRATION_TLS_PORT:-0}"
TOKEN="${GATEWAY_INTERNAL_TOKEN:-gateway-container-integration-token}"
REGISTRY_TOKEN="${REGISTRY_TOKEN:-registry-container-integration-token}"
BASE_URL=""
TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/gateway-container-integration.XXXXXX")"
HEADERS="$TEMP_DIR/headers"
BODY="$TEMP_DIR/body"

compose() {
    docker compose -p "$PROJECT" -f "$COMPOSE_FILE" "$@"
}

cleanup() {
    compose down --volumes --remove-orphans >/dev/null 2>&1 || true
    rm -rf "$TEMP_DIR"
}

trap cleanup EXIT INT TERM

wait_for() {
    for _ in $(seq 1 90); do
        if curl -ksf --max-time 1 "$BASE_URL/health" >/dev/null 2>&1; then
            return 0
        fi
        sleep 1
    done

    compose logs >&2
    return 1
}

compose up --build --detach
TLS_PORT="$(compose port tls 8443 | sed -n '1{s/.*://;p;}')"
if [[ ! "$TLS_PORT" =~ ^[1-9][0-9]*$ ]]; then
    echo "Could not resolve the TLS edge host port." >&2
    compose logs >&2
    exit 1
fi
BASE_URL="https://localhost:$TLS_PORT"
wait_for

if compose port planner 8080 >/dev/null 2>&1; then
    echo "Planner must not publish its private port to the host." >&2
    exit 1
fi

internal_status="$(curl -ksS --max-time 5 -o /dev/null -w '%{http_code}' \
    -X POST "$BASE_URL/_internal/gateway/plan")"
if [[ "$internal_status" != "404" ]]; then
    echo "Internal planner route leaked through the public TLS edge: HTTP $internal_status" >&2
    exit 1
fi

unauthorized_status="$(curl -ksS --max-time 5 -o /dev/null -w '%{http_code}' \
    -X POST -H 'Content-Type: application/json' --data '{}' \
    "$BASE_URL/registry/endpoints")"
if [[ "$unauthorized_status" != "401" ]]; then
    echo "Registry API accepted an unauthenticated request: HTTP $unauthorized_status" >&2
    exit 1
fi

curl -ksS --max-time 5 -D "$HEADERS" -o "$BODY" \
    -X POST \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    -H 'Content-Type: application/json' \
    --data '{"client":"http-smoke","external_id":"container-provider","endpoint_key":"http-smoke-endpoint","managed":true}' \
    "$BASE_URL/registry/endpoints"

endpoint_status="$(awk '/^HTTP\// { status = $2 } END { print status }' "$HEADERS")"
endpoint_key="$(php -r '$value=json_decode(file_get_contents($argv[1]), true, 512, JSON_THROW_ON_ERROR); echo $value["endpoint_key"] ?? "";' "$BODY")"
if [[ "$endpoint_status" != "201" || "$endpoint_key" != "http-smoke-endpoint" ]]; then
    echo "Container registry endpoint creation failed: status=$endpoint_status body=$(<"$BODY")" >&2
    exit 1
fi

curl -ksS --max-time 5 -D "$HEADERS" -o "$BODY" \
    -X POST \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    -H 'Content-Type: application/json' \
    --data '{"subscriber_id":"container-subscriber","subscription_id":"container-subscription","webhook_group":"container-smoke","routing_scope":"application","routing_key":"app-123","url":"http://planner:8080/http-smoke/receiver","match":{"version":"v1","rules":{"body.event":["required","in:container"]}}}' \
    "$BASE_URL/registry/endpoints/$endpoint_key/subscriptions"

subscription_status="$(awk '/^HTTP\// { status = $2 } END { print status }' "$HEADERS")"
if [[ "$subscription_status" != "201" ]]; then
    echo "Container subscription creation failed: status=$subscription_status body=$(<"$BODY")" >&2
    exit 1
fi

ingress_status="$(curl -ksS --max-time 5 -o "$BODY" -w '%{http_code}' \
    -X POST -H 'Content-Type: application/json' --data '{"event":"container"}' \
    "$BASE_URL/http-smoke/registry-ingress")"
if [[ "$ingress_status" != "202" ]]; then
    echo "Container registry ingress was not accepted: HTTP $ingress_status body=$(<"$BODY")" >&2
    exit 1
fi

sleep 1

if compose exec -T planner grep -Fx '{"event":"container"}' /tmp/receiver.txt >/dev/null 2>&1; then
    echo "Gateway delivered to a private subscriber address." >&2
    exit 1
fi

if ! compose logs gateway | grep -F 'target planner resolves only to blocked addresses' >/dev/null; then
    echo "Gateway did not report the expected private-address delivery denial." >&2
    compose logs >&2
    exit 1
fi

echo "Gateway container integration smoke passed."
