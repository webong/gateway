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
    local status=$?
    if [[ "$status" -ne 0 ]]; then
        compose logs --tail 100 >&2 || true
    fi
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

internal_status="$(curl -ksS --max-time 15 -o /dev/null -w '%{http_code}' \
    -X POST "$BASE_URL/_internal/gateway/plan")"
if [[ "$internal_status" != "404" ]]; then
    echo "Internal planner route leaked through the public TLS edge: HTTP $internal_status" >&2
    exit 1
fi

unauthorized_status="$(curl -ksS --max-time 15 -o /dev/null -w '%{http_code}' \
    -X POST -H 'Content-Type: application/json' --data '{}' \
    "$BASE_URL/registry/endpoints")"
if [[ "$unauthorized_status" != "401" ]]; then
    echo "Registry API accepted an unauthenticated request: HTTP $unauthorized_status" >&2
    exit 1
fi

curl -ksS --max-time 15 -D "$HEADERS" -o "$BODY" \
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

curl -ksS --max-time 15 -D "$HEADERS" -o "$BODY" \
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

ingress_status="$(curl -ksS --max-time 15 -o "$BODY" -w '%{http_code}' \
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

# Exercise public TLS -> Spinner -> Planner -> private durable Spinner queue
# -> SMTP relay. The queue's 202 means durable acceptance; the relay check
# below separately proves that delivery completed.
policy_status="$(curl -ksS --max-time 10 -o "$BODY" -w '%{http_code}' \
    -X POST -H "Authorization: Bearer $REGISTRY_TOKEN" -H 'Content-Type: application/json' \
    --data '{"http_submission_enabled":true}' "$BASE_URL/email/runtime-policy")"
if [[ "$policy_status" != "201" ]]; then
    echo "Container Email runtime policy setup failed: status=$policy_status body=$(<"$BODY")" >&2
    exit 1
fi
quota_status="$(curl -ksS --max-time 10 -o "$BODY" -w '%{http_code}' \
    -X POST -H "Authorization: Bearer $REGISTRY_TOKEN" -H 'Content-Type: application/json' \
    --data '{"domain_daily_limit":1}' "$BASE_URL/email/submission-policy")"
if [[ "$quota_status" != "201" ]]; then
    echo "Container Email submission quota setup failed: status=$quota_status body=$(<"$BODY")" >&2
    exit 1
fi
email_body="$(php -r 'echo json_encode([
    "mail_from" => "sender@example.test",
    "recipients" => ["recipient@example.test"],
    "message_base64" => base64_encode("From: sender@example.test\r\nSubject: container smoke\r\n\r\nhello"),
], JSON_THROW_ON_ERROR);')"
email_status="$(curl -ksS --max-time 10 -o "$BODY" -w '%{http_code}' \
    -u 'smoke-sender:smoke-secret' \
    -H 'Idempotency-Key: container-email-1' -H 'Content-Type: application/json' \
    --data "$email_body" "$BASE_URL/email/submissions")"
email_id="$(php -r '$value=json_decode(file_get_contents($argv[1]), true); echo $value["id"] ?? "";' "$BODY")"
if [[ "$email_status" != "202" || ! "$email_id" =~ ^[a-f0-9]{32}$ ]]; then
    echo "Container HTTP email submission failed: status=$email_status body=$(<"$BODY")" >&2
    compose logs >&2
    exit 1
fi

for _ in $(seq 1 60); do
    if compose exec -T relay test -s /tmp/accepted.jsonl >/dev/null 2>&1; then
        break
    fi
    sleep 0.25
done
relay_rows="$(compose exec -T relay cat /tmp/accepted.jsonl)"
php -r '
    $rows = array_values(array_filter(explode("\n", trim(stream_get_contents(STDIN)))));
    $mail = count($rows) === 1 ? json_decode($rows[0], true) : null;
    if (!is_array($mail) || ($mail["from"] ?? null) !== "sender@example.test"
        || ($mail["recipients"] ?? null) !== ["recipient@example.test"]
        || !str_contains($mail["data"] ?? "", "Subject: container smoke")) {
        fwrite(STDERR, "Container SMTP relay did not receive exactly one complete email.\n");
        exit(1);
    }
' <<< "$relay_rows"

compose restart gateway >/dev/null
wait_for
receipt_status="$(curl -ksS --max-time 10 -o "$BODY" -w '%{http_code}' \
    -H "Authorization: Bearer $REGISTRY_TOKEN" "$BASE_URL/deliveries/$email_id")"
receipt_id="$(php -r '$value=json_decode(file_get_contents($argv[1]), true); echo $value["id"] ?? "";' "$BODY")"
receipt_state="$(php -r '$value=json_decode(file_get_contents($argv[1]), true); echo $value["state"] ?? "";' "$BODY")"
if [[ "$receipt_status" != "200" || "$receipt_id" != "$email_id" || "$receipt_state" != "delivered" ]]; then
    echo "Container restart lost the durable email receipt: status=$receipt_status body=$(<"$BODY")" >&2
    exit 1
fi
replay_status="$(curl -ksS --max-time 10 -o "$BODY" -w '%{http_code}' \
    -u 'smoke-sender:smoke-secret' \
    -H 'Idempotency-Key: container-email-1' -H 'Content-Type: application/json' \
    --data "$email_body" "$BASE_URL/email/submissions")"
replay_id="$(php -r '$value=json_decode(file_get_contents($argv[1]), true); echo $value["id"] ?? "";' "$BODY")"
if [[ "$replay_status" != "202" || "$replay_id" != "$email_id" ]]; then
    echo "Container HTTP email replay changed its receipt: status=$replay_status body=$(<"$BODY")" >&2
    exit 1
fi
relay_count="$(compose exec -T relay sh -c 'wc -l < /tmp/accepted.jsonl')"
if [[ "$relay_count" != "1" ]]; then
    echo "Container HTTP email replay sent the message to the relay more than once: $relay_count copies" >&2
    exit 1
fi

changed_body="$(php -r 'echo json_encode([
    "mail_from" => "sender@example.test",
    "recipients" => ["recipient@example.test"],
    "message_base64" => base64_encode("From: sender@example.test\r\nSubject: changed\r\n\r\nhello"),
], JSON_THROW_ON_ERROR);')"
changed_status="$(curl -ksS --max-time 10 -o "$BODY" -w '%{http_code}' \
    -u 'smoke-sender:smoke-secret' \
    -H 'Idempotency-Key: container-email-1' -H 'Content-Type: application/json' \
    --data "$changed_body" "$BASE_URL/email/submissions")"
if [[ "$changed_status" != "409" ]]; then
    echo "Container HTTP email submission accepted changed bytes under one key: HTTP $changed_status" >&2
    exit 1
fi

new_key_status="$(curl -ksS --max-time 10 -o "$BODY" -w '%{http_code}' \
    -u 'smoke-sender:smoke-secret' \
    -H 'Idempotency-Key: container-email-2' -H 'Content-Type: application/json' \
    --data "$email_body" "$BASE_URL/email/submissions")"
if [[ "$new_key_status" != "429" ]]; then
    echo "Container HTTP email submission did not enforce the daily account quota: HTTP $new_key_status" >&2
    exit 1
fi

second_body="$(php -r 'echo json_encode([
    "mail_from" => "second@example.test",
    "recipients" => ["recipient@example.test"],
    "message_base64" => base64_encode("From: second@example.test\r\nSubject: second account\r\n\r\nhello"),
], JSON_THROW_ON_ERROR);')"
second_status="$(curl -ksS --max-time 10 -o "$BODY" -w '%{http_code}' \
    -u 'smoke-second:smoke-secret' \
    -H 'Idempotency-Key: container-second-account' -H 'Content-Type: application/json' \
    --data "$second_body" "$BASE_URL/email/submissions")"
if [[ "$second_status" != "429" ]]; then
    echo "Container HTTP email submission did not share the sender-domain cap across accounts: HTTP $second_status" >&2
    exit 1
fi
relay_count="$(compose exec -T relay sh -c 'wc -l < /tmp/accepted.jsonl')"
if [[ "$relay_count" != "1" ]]; then
    echo "Container sender-domain quota leaked another message to the relay: $relay_count copies" >&2
    exit 1
fi

# A different authenticated sender/domain can still submit. Refuse its
# recipient at the disposable relay and verify the opt-in, null-path DSN.
notice_body="$(php -r 'echo json_encode([
    "mail_from" => "notice@other.test",
    "recipients" => ["refuse@example.net"],
    "message_base64" => base64_encode("From: notice@other.test\r\nSubject: failed relay\r\n\r\nhello"),
], JSON_THROW_ON_ERROR);')"
notice_status="$(curl -ksS --max-time 10 -o "$BODY" -w '%{http_code}' \
    -u 'smoke-notice:smoke-secret' \
    -H 'Idempotency-Key: container-failed-relay' -H 'Content-Type: application/json' \
    --data "$notice_body" "$BASE_URL/email/submissions")"
failed_id="$(php -r '$value=json_decode(file_get_contents($argv[1]), true); echo $value["id"] ?? "";' "$BODY")"
if [[ "$notice_status" != "202" || ! "$failed_id" =~ ^[a-f0-9]{32}$ ]]; then
    echo "Container failure-notice submission was not durably accepted: status=$notice_status body=$(<"$BODY")" >&2
    exit 1
fi
failed_state=""
failure_notice_id=""
for _ in $(seq 1 60); do
    curl -ksS --max-time 10 -o "$BODY" \
        -H "Authorization: Bearer $REGISTRY_TOKEN" "$BASE_URL/deliveries/$failed_id"
    failed_state="$(php -r '$value=json_decode(file_get_contents($argv[1]), true); echo $value["state"] ?? "";' "$BODY")"
    failure_notice_id="$(php -r '$value=json_decode(file_get_contents($argv[1]), true); echo $value["failure_notice_id"] ?? "";' "$BODY")"
    if [[ "$failed_state" == "failed" && "$failure_notice_id" =~ ^[a-f0-9]{32}$ ]]; then
        break
    fi
    sleep 0.25
done
if [[ "$failed_state" != "failed" || ! "$failure_notice_id" =~ ^[a-f0-9]{32}$ ]]; then
    echo "Container failed relay did not record a durable failure notice: body=$(<"$BODY")" >&2
    exit 1
fi
for _ in $(seq 1 60); do
    relay_count="$(compose exec -T relay sh -c 'wc -l < /tmp/accepted.jsonl')"
    if [[ "$relay_count" == "2" ]]; then
        break
    fi
    sleep 0.25
done
relay_rows="$(compose exec -T relay cat /tmp/accepted.jsonl)"
php -r '
    $rows = array_values(array_filter(explode("\n", trim(stream_get_contents(STDIN)))));
    $notice = count($rows) === 2 ? json_decode($rows[1], true) : null;
    if (!is_array($notice) || ($notice["from"] ?? null) !== ""
        || ($notice["recipients"] ?? null) !== ["notice@other.test"]
        || !str_contains($notice["data"] ?? "", "Delivery ID: ".$argv[1])
        || !str_contains($notice["data"] ?? "", "Subject: Delivery Status Notification (Failure)")) {
        fwrite(STDERR, "Container relay did not receive the correct null-path failure notice.\n");
        exit(1);
    }
' "$failed_id" <<< "$relay_rows"

echo "Gateway container integration smoke passed."
