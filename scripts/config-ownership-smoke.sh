#!/usr/bin/env bash

# Disposable configuration-ownership verification across two Spinner nodes.
#
# It starts one Planner PHP fixture and two Spinner processes that share it, then
# checks that Planner-managed desired state is owned by Planner and observed by
# the Spinners without redeploying them:
#
#   * Shell local hook grants: an already-running Spinner pair honors a grant
#     created after they started, and both stop executing once it is revoked.
#   * Webstream private-source and route-policy resources: the trusted decision
#     each Spinner fetches per request carries the active grant only, and a
#     route policy is isolated to its exact host and path.
#   * Schedule notification destination: a singleton resource rotates through
#     revisions, rejects stale writes, and never exposes its token.
#   * Mail upstream, DKIM, hosted-adapter, and mailbox resources: the private
#     resolution contract each Spinner calls per attempt reflects creation,
#     rotation, and revocation, and no secret is stored in plaintext.
#
# It never touches a production host, needs no root, and preserves diagnostics
# on failure.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_PATH="${GATEWAY_CONFIG_APP_PATH:-$ROOT/tests/Fixtures/octane-app}"
PHP_PORT="${GATEWAY_CONFIG_PHP_PORT:-18341}"
A_HTTP_PORT="${GATEWAY_CONFIG_A_HTTP_PORT:-18342}"
B_HTTP_PORT="${GATEWAY_CONFIG_B_HTTP_PORT:-18343}"
TOKEN="${GATEWAY_INTERNAL_TOKEN:-config-smoke-token}"
REGISTRY_TOKEN="${REGISTRY_TOKEN:-config-registry-smoke-token}"
SHELL_TOKEN="${GATEWAY_SHELL_SUBSCRIPTION_TOKEN:-config-shell-subscription-token}"
TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/gateway-config.XXXXXX")"
DB_FILE="$TEMP_DIR/gateway.sqlite"
PHP_LOG="$TEMP_DIR/php.log"
A_LOG="$TEMP_DIR/spinner-a.log"
B_LOG="$TEMP_DIR/spinner-b.log"
GATEWAY_BIN="$TEMP_DIR/gateway"
HOOKS_DIR="$TEMP_DIR/hooks"
RESP_HEADERS="$TEMP_DIR/resp.headers"
RESP_BODY="$TEMP_DIR/resp.body"
PHP_PID=""
A_PID=""
B_PID=""

cleanup() {
    local status=$?
    for pid in "$B_PID" "$A_PID" "$PHP_PID"; do
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
        fi
    done
    if [[ "$status" -ne 0 ]]; then
        echo "Config-ownership smoke diagnostics preserved at $TEMP_DIR" >&2
        return
    fi
    case "$TEMP_DIR" in
        "${TMPDIR:-/tmp}"/gateway-config.*) rm -r -- "$TEMP_DIR" ;;
    esac
}
trap cleanup EXIT INT TERM

if [[ ! -f "$APP_PATH/bootstrap/app.php" || ! -f "$ROOT/vendor/autoload.php" ]]; then
    echo 'Laravel fixture and Composer dependencies are required.' >&2
    exit 1
fi
for tool in curl php; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        echo "$tool is required for the config-ownership smoke." >&2
        exit 1
    fi
done

touch "$DB_FILE"
GO_BUILD_FLAGS="${GOFLAGS:--mod=mod}"
go build -tags gateway_shell "$GO_BUILD_FLAGS" -o "$GATEWAY_BIN" "$ROOT/src/spinner/cmd/proxy"

mkdir -p "$HOOKS_DIR/github"
cat > "$HOOKS_DIR/github/deploy" <<'HOOK'
#!/bin/sh
cat >/dev/null
printf '%s' '{"response":{"status":202,"body":"deploy-ok"}}'
HOOK
chmod 0755 "$HOOKS_DIR/github/deploy"

# --- Planner fixture --------------------------------------------------------
(
    cd "$APP_PATH"
    APP_ENV=testing APP_KEY="base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" \
    COMPOSER_VENDOR_DIR="$ROOT/vendor" DB_CONNECTION=sqlite DB_DATABASE="$DB_FILE" \
    GATEWAY_HTTP_REGISTRY_SMOKE=1 GATEWAY_EMAIL_SMOKE=1 \
    GATEWAY_PLANNER='Webong\Gateway\Tests\Fixtures\HttpSmokePlanner' \
    GATEWAY_PATH_RESOLVER='Webong\Gateway\Tests\Fixtures\HttpSmokePathResolver' \
    GATEWAY_WEBSTREAM_REPLY_URL='http://events.internal:8080/events' \
    GATEWAY_INTERNAL_TOKEN="$TOKEN" REGISTRY_TOKEN="$REGISTRY_TOKEN" \
    GATEWAY_SHELL_SUBSCRIPTION_TOKEN="$SHELL_TOKEN" \
    GATEWAY_SCHEDULE_NOTIFICATION_ALLOWED_HOSTS='127.0.0.1' \
    GATEWAY_EMAIL_UPSTREAM_ALLOWED_HOSTS='127.0.0.1' \
    GATEWAY_EMAIL_HOSTED_ALLOWED_HOSTS='provider.smoke.test' \
    exec php -S "127.0.0.1:$PHP_PORT" -t public public/index.php
) >"$PHP_LOG" 2>&1 &
PHP_PID=$!

wait_http() {
    local url="$1" log="$2" pid="$3"
    for _ in $(seq 1 60); do
        if curl -fsS --max-time 1 "$url" >/dev/null 2>&1; then return 0; fi
        if ! kill -0 "$pid" 2>/dev/null; then break; fi
        sleep 0.25
    done
    tail -80 "$log" >&2
    return 1
}
wait_http "http://127.0.0.1:$PHP_PORT/http-smoke/worker" "$PHP_LOG" "$PHP_PID"

# --- Two Spinner nodes sharing one Planner ----------------------------------
start_spinner() {
    local port="$1" log="$2"
    GATEWAY_RUNTIME=http GATEWAY_LARAVEL_BACKEND_URL="http://127.0.0.1:$PHP_PORT" \
    GATEWAY_INTERNAL_TOKEN="$TOKEN" PORT="$port" LOG_LEVEL=error \
    GATEWAY_SHELL_ENABLED=true GATEWAY_SHELL_HOOKS_DIR="$HOOKS_DIR" \
    GATEWAY_SHELL_OUTPUT_MODE=outcome GATEWAY_SHELL_TIMEOUT=10s \
    "$GATEWAY_BIN" >"$log" 2>&1 &
    echo $!
}
A_PID="$(start_spinner "$A_HTTP_PORT" "$A_LOG")"
B_PID="$(start_spinner "$B_HTTP_PORT" "$B_LOG")"
wait_http "http://127.0.0.1:$A_HTTP_PORT/health" "$A_LOG" "$A_PID"
wait_http "http://127.0.0.1:$B_HTTP_PORT/health" "$B_LOG" "$B_PID"

planner="http://127.0.0.1:$PHP_PORT"
edge_a="http://127.0.0.1:$A_HTTP_PORT"
edge_b="http://127.0.0.1:$B_HTTP_PORT"
auth="Authorization: Bearer $REGISTRY_TOKEN"
shell_auth="X-Gateway-Shell-Subscription: $SHELL_TOKEN"
internal="X-Gateway-Internal: $TOKEN"

CODE=""
request() {
    local method="$1" url="$2"; shift 2
    CODE="$(curl -sS -o "$RESP_BODY" -D "$RESP_HEADERS" -w '%{http_code}' -X "$method" "$url" "$@")"
}
etag_of() {
    awk -v name='ETag:' 'tolower($1) == tolower(name) { sub(/\r$/, "", $2); print $2; exit }' "$RESP_HEADERS"
}
field() {
    php -r '$v=json_decode(file_get_contents($argv[1]), true, 512, JSON_THROW_ON_ERROR); $h=$v; foreach (array_slice($argv,2) as $k) { $h = $h[$k] ?? null; } echo is_bool($h) ? ($h?"true":"false") : (is_scalar($h) ? (string) $h : "");' "$RESP_BODY" "$@"
}
fail() {
    echo "$1" >&2
    cat "$A_LOG" "$B_LOG" "$PHP_LOG" >&2 || true
    exit 1
}

# ---------------------------------------------------------------------------
# 1. Shell local hook grants propagate to both already-running Spinners and are
#    revoked without redeploying either node.
# ---------------------------------------------------------------------------
endpoint_key="http-smoke-endpoint"
request POST "$edge_a/registry/endpoints" -H "$auth" -H 'Content-Type: application/json' \
    -d "{\"client\":\"http-smoke\",\"external_id\":\"config-smoke-provider\",\"endpoint_key\":\"$endpoint_key\",\"managed\":true}"
[[ "$CODE" == "201" || "$CODE" == "200" ]] || fail "Endpoint creation failed: $CODE $(cat "$RESP_BODY")"

# A subscription is refused while no grant exists.
request POST "$edge_a/registry/endpoints/$endpoint_key/subscriptions" -H "$auth" -H "$shell_auth" \
    -H 'Content-Type: application/json' \
    -d "{\"subscriber_id\":\"deploy-owner\",\"subscription_id\":\"deploy\",\"type\":\"reply\",\"target_kind\":\"shell\",\"hook\":\"github/deploy\",\"webhook_group\":\"config-smoke\",\"routing_scope\":\"application\",\"routing_key\":\"app-123\"}"
[[ "$CODE" =~ ^4[0-9][0-9]$ ]] || fail "Ungranted Shell subscription was accepted: $CODE $(cat "$RESP_BODY")"

# The operator grants the exact endpoint/subscriber/hook after both nodes are up.
request POST "$planner/shell/hook-grants" -H "$auth" -H "$shell_auth" -H 'Content-Type: application/json' \
    -d "{\"endpoint_key\":\"$endpoint_key\",\"subscriber_id\":\"deploy-owner\",\"hook\":\"github/deploy\"}"
[[ "$CODE" == "201" ]] || fail "Shell hook grant failed: $CODE $(cat "$RESP_BODY")"
grant_id="$(field id)"
grant_etag="$(etag_of)"

request POST "$edge_a/registry/endpoints/$endpoint_key/subscriptions" -H "$auth" -H "$shell_auth" \
    -H 'Content-Type: application/json' \
    -d "{\"subscriber_id\":\"deploy-owner\",\"subscription_id\":\"deploy\",\"type\":\"reply\",\"target_kind\":\"shell\",\"hook\":\"github/deploy\",\"webhook_group\":\"config-smoke\",\"routing_scope\":\"application\",\"routing_key\":\"app-123\"}"
[[ "$CODE" == "201" ]] || fail "Shell subscription failed: $CODE $(cat "$RESP_BODY")"
sub_id="$(field id)"
sub_etag="$(etag_of)"

ingress() {
    local edge="$1"
    request POST "$edge/http-smoke/registry-ingress" -H 'Content-Type: application/json' -d '{"event":"push"}'
    echo "$CODE $(cat "$RESP_BODY")"
}
[[ "$(ingress "$edge_a")" == "202 deploy-ok" ]] || fail "Spinner A did not execute the granted local hook."
[[ "$(ingress "$edge_b")" == "202 deploy-ok" ]] || fail "Spinner B did not execute the granted local hook."

# Revoking the grant disables execution on both nodes without a restart.
request DELETE "$planner/shell/hook-grants/$grant_id" -H "$auth" -H "$shell_auth" -H "If-Match: $grant_etag"
[[ "$CODE" == "204" ]] || fail "Shell hook grant revocation failed: $CODE"
[[ "$(ingress "$edge_a")" != "202 deploy-ok" ]] || fail "Spinner A still executed a revoked local hook."
[[ "$(ingress "$edge_b")" != "202 deploy-ok" ]] || fail "Spinner B still executed a revoked local hook."

request DELETE "$edge_a/registry/endpoints/$endpoint_key/subscriptions/$sub_id" -H "$auth" -H "$shell_auth" -H "If-Match: $sub_etag"
[[ "$CODE" == "204" ]] || fail "Shell subscription cleanup failed: $CODE"

# ---------------------------------------------------------------------------
# 2. Webstream private-source grants and route policies in the per-request
#    trusted decision.
# ---------------------------------------------------------------------------
stream_event() {
    local host="$1" method="$2" attributes="$3"
    request POST "$planner/_internal/gateway/event" -H "$internal" -H 'Content-Type: application/json' \
        -d "{\"id\":\"stream-1\",\"protocol\":\"webstream\",\"kind\":\"connect\",\"route\":\"/http-smoke/stream-source\",\"host\":\"$host\",\"method\":\"$method\",\"attributes\":$attributes}"
}
stream_cidrs() {
    php -r '$v=json_decode(file_get_contents($argv[1]), true, 512, JSON_THROW_ON_ERROR); echo $v["stream"]["attributes"]["private_source_cidrs"] ?? "<none>";' "$RESP_BODY"
}
stream_action() {
    php -r '$v=json_decode(file_get_contents($argv[1]), true, 512, JSON_THROW_ON_ERROR); echo $v["action"] ?? "<none>";' "$RESP_BODY"
}

request POST "$planner/webstream/private-sources" -H "$auth" -H 'Content-Type: application/json' \
    -d '{"origin":"http://events.internal:8080","cidrs":["10.42.1.0/24"]}'
[[ "$CODE" == "201" ]] || fail "Private-source creation failed: $CODE $(cat "$RESP_BODY")"
source_id="$(field id)"
source_etag="$(etag_of)"
stream_event 'gateway.test' 'GET' '{}'
[[ "$CODE" == "200" ]] || fail "Webstream decision failed: $CODE $(cat "$RESP_BODY")"
[[ "$(stream_cidrs)" == "10.42.1.0/24" ]] || fail "Active private-source grant missing from the decision."

request PUT "$planner/webstream/private-sources/$source_id" -H "$auth" -H "If-Match: $source_etag" \
    -H 'Content-Type: application/json' -d '{"origin":"http://events.internal:8080","cidrs":["10.42.1.0/24"],"enabled":false}'
[[ "$CODE" == "200" ]] || fail "Private-source disable failed: $CODE $(cat "$RESP_BODY")"
stream_event 'gateway.test' 'GET' '{}'
[[ "$(stream_cidrs)" == "" ]] || fail "Disabled private-source grant still present in the decision."

request POST "$planner/webstream/routes" -H "$auth" -H 'Content-Type: application/json' \
    -d '{"host":"gateway.test","path":"/http-smoke/stream-source","source":"http://events.internal:8080/events","upload":true}'
[[ "$CODE" == "201" ]] || fail "Route-policy creation failed: $CODE $(cat "$RESP_BODY")"
policy_id="$(field id)"
policy_etag="$(etag_of)"
stream_event 'gateway.test' 'POST' '{"upload_mode":"stream"}'
[[ "$(stream_action)" == "stream" ]] || fail "Matching route policy did not permit the upload."
stream_event 'other.test' 'POST' '{"upload_mode":"stream"}'
[[ "$(stream_action)" == "reject" ]] || fail "Route policy leaked to a different host."

request PUT "$planner/webstream/routes/$policy_id" -H "$auth" -H "If-Match: $policy_etag" \
    -H 'Content-Type: application/json' \
    -d '{"host":"gateway.test","path":"/http-smoke/stream-source","source":"https://other.test/events","upload":true}'
[[ "$CODE" == "200" ]] || fail "Route-policy rotation failed: $CODE $(cat "$RESP_BODY")"
stream_event 'gateway.test' 'POST' '{"upload_mode":"stream"}'
[[ "$(stream_action)" == "reject" ]] || fail "Rotated route policy still permitted the old source."

# ---------------------------------------------------------------------------
# 3. Schedule notification singleton: revision-guarded rotation and no token
#    exposure. Dispatch itself is covered by ScheduleNotificationsTest.
# ---------------------------------------------------------------------------
request POST "$planner/schedules/notification-destination" -H "$auth" -H 'Content-Type: application/json' \
    -d '{"url":"http://127.0.0.1:18344/hook","token":"notify-one"}'
[[ "$CODE" == "201" ]] || fail "Schedule notification creation failed: $CODE $(cat "$RESP_BODY")"
notify_etag="$(etag_of)"
[[ "$(field token_configured)" == "true" ]] || fail "Schedule notification lost its token flag."
if grep -q 'notify-one' "$RESP_BODY"; then fail "Schedule notification response exposed its token."; fi

request GET "$planner/configurations/schedule.notification/$(field id)/revisions" -H "$auth"
[[ "$CODE" == "200" ]] || fail "Schedule notification revisions failed: $CODE"
if grep -q 'notify-one' "$RESP_BODY"; then fail "Schedule notification revisions exposed its token."; fi

request PUT "$planner/schedules/notification-destination" -H "$auth" -H "If-Match: $notify_etag" \
    -H 'Content-Type: application/json' -d '{"url":"http://127.0.0.1:18344/hook","token":"notify-two"}'
[[ "$CODE" == "200" ]] || fail "Schedule notification rotation failed: $CODE $(cat "$RESP_BODY")"
new_etag="$(etag_of)"
[[ -n "$new_etag" && "$new_etag" != "$notify_etag" ]] || fail "Schedule notification rotation did not advance the revision."

request PUT "$planner/schedules/notification-destination" -H "$auth" -H "If-Match: $notify_etag" \
    -H 'Content-Type: application/json' -d '{"url":"http://127.0.0.1:18344/hook","token":"stale"}'
[[ "$CODE" == "412" ]] || fail "Stale schedule notification write was accepted: $CODE"

request POST "$planner/schedules/notification-destination" -H "$auth" -H 'Content-Type: application/json' \
    -d '{"url":"https://evil.test/hook"}'
[[ "$CODE" == "422" ]] || fail "Schedule notification accepted an undeployed host: $CODE"

request DELETE "$planner/schedules/notification-destination" -H "$auth" -H "If-Match: $new_etag"
[[ "$CODE" == "204" ]] || fail "Schedule notification deletion failed: $CODE"

# ---------------------------------------------------------------------------
# 4. Mail resources through the private resolution contract each Spinner calls
#    per delivery attempt: creation, rotation, revocation, and provider health.
# ---------------------------------------------------------------------------
request POST "$planner/email/upstreams" -H "$auth" -H 'Content-Type: application/json' \
    -d '{"protocol":"imap","name":"mail","address":"127.0.0.1:1","server_name":"127.0.0.1","network_scope":"private"}'
[[ "$CODE" == "201" ]] || fail "IMAP upstream creation failed: $CODE $(cat "$RESP_BODY")"
request POST "$planner/email/upstreams" -H "$auth" -H 'Content-Type: application/json' \
    -d '{"protocol":"smtp","name":"mail","address":"127.0.0.1:1","server_name":"127.0.0.1","tls_mode":"starttls","network_scope":"private"}'
[[ "$CODE" == "201" ]] || fail "SMTP upstream creation failed: $CODE $(cat "$RESP_BODY")"
request POST "$planner/email/upstreams" -H "$auth" -H 'Content-Type: application/json' \
    -d '{"protocol":"imap","name":"other","address":"other.test:993","server_name":"other.test"}'
[[ "$CODE" == "201" ]] || fail "Planner rejected a new public mail host without a deployment change: $CODE"
request POST "$planner/email/upstreams" -H "$auth" -H 'Content-Type: application/json' \
    -d '{"protocol":"imap","name":"bad","address":"other.test:0","server_name":"other.test"}'
[[ "$CODE" == "422" ]] || fail "Planner accepted an invalid mail endpoint: $CODE"

pem1_file="$TEMP_DIR/dkim-1.pem"
pem2_file="$TEMP_DIR/dkim-2.pem"
php -r '$k=openssl_pkey_new(["private_key_type"=>OPENSSL_KEYTYPE_RSA,"private_key_bits"=>2048]); openssl_pkey_export($k,$p); file_put_contents($argv[1], $p);' "$pem1_file"
php -r '$k=openssl_pkey_new(["private_key_type"=>OPENSSL_KEYTYPE_RSA,"private_key_bits"=>2048]); openssl_pkey_export($k,$p); file_put_contents($argv[1], $p);' "$pem2_file"
dkim_body() {
    DKIM_PEM_FILE="$1" DKIM_SELECTOR="$2" php -r '
        $data = ["domain" => "managed.test", "selector" => getenv("DKIM_SELECTOR"),
            "private_key_pem" => file_get_contents(getenv("DKIM_PEM_FILE"))];
        echo json_encode($data, JSON_THROW_ON_ERROR);
    '
}
request POST "$planner/email/dkim-keys" -H "$auth" -H 'Content-Type: application/json' -d "$(dkim_body "$pem1_file" sel1)"
[[ "$CODE" == "201" ]] || fail "DKIM key creation failed: $CODE $(cat "$RESP_BODY")"
dkim_id="$(field id)"
dkim_etag="$(etag_of)"
request GET "$planner/_internal/gateway/email/dkim/managed.test" -H "$internal"
[[ "$CODE" == "200" && "$(field selector)" == "sel1" && "$(field private_key_pem)" == "$(cat "$pem1_file")" ]] \
    || fail "DKIM resolution after creation failed: $CODE"

request PUT "$planner/email/dkim-keys/$dkim_id" -H "$auth" -H "If-Match: $dkim_etag" \
    -H 'Content-Type: application/json' -d "$(dkim_body "$pem2_file" sel2)"
[[ "$CODE" == "200" ]] || fail "DKIM key rotation failed: $CODE $(cat "$RESP_BODY")"
rotated_dkim_etag="$(etag_of)"
request GET "$planner/_internal/gateway/email/dkim/managed.test" -H "$internal"
[[ "$(field selector)" == "sel2" && "$(field private_key_pem)" == "$(cat "$pem2_file")" ]] \
    || fail "DKIM resolution after rotation returned the old key."

request PUT "$planner/email/dkim-keys/$dkim_id" -H "$auth" -H "If-Match: $dkim_etag" \
    -H 'Content-Type: application/json' -d '{"domain":"managed.test","selector":"sel3"}'
[[ "$CODE" == "412" ]] || fail "Stale DKIM rotation was accepted: $CODE"

request PUT "$planner/email/dkim-keys/$dkim_id" -H "$auth" -H "If-Match: $rotated_dkim_etag" \
    -H 'Content-Type: application/json' -d '{"domain":"managed.test","selector":"sel2","enabled":false}'
[[ "$CODE" == "200" ]] || fail "DKIM key disable failed: $CODE $(cat "$RESP_BODY")"
request GET "$planner/_internal/gateway/email/dkim/managed.test" -H "$internal"
[[ "$CODE" == "404" ]] || fail "Disabled DKIM key was still resolvable: $CODE"

request POST "$planner/email/hosted-adapters" -H "$auth" -H 'Content-Type: application/json' \
    -d '{"name":"smoke.adapter","url":"https://provider.smoke.test/inbound","bearer_token":"tok-one"}'
[[ "$CODE" == "201" ]] || fail "Hosted-adapter creation failed: $CODE $(cat "$RESP_BODY")"
adapter_id="$(field id)"
adapter_etag="$(etag_of)"
request GET "$planner/_internal/gateway/email/hosted-adapters/smoke.adapter" -H "$internal"
[[ "$CODE" == "200" && "$(field bearer_token)" == "tok-one" ]] || fail "Hosted-adapter resolution after creation failed: $CODE"

request PUT "$planner/email/hosted-adapters/$adapter_id" -H "$auth" -H "If-Match: $adapter_etag" \
    -H 'Content-Type: application/json' \
    -d '{"name":"smoke.adapter","url":"https://provider.smoke.test/inbound","bearer_token":"tok-two"}'
[[ "$CODE" == "200" ]] || fail "Hosted-adapter token rotation failed: $CODE $(cat "$RESP_BODY")"
rotated_adapter_etag="$(etag_of)"
request GET "$planner/_internal/gateway/email/hosted-adapters/smoke.adapter" -H "$internal"
[[ "$(field bearer_token)" == "tok-two" ]] || fail "Hosted-adapter resolution after rotation returned the old token."

request PUT "$planner/email/hosted-adapters/$adapter_id" -H "$auth" -H "If-Match: $rotated_adapter_etag" \
    -H 'Content-Type: application/json' \
    -d '{"name":"smoke.adapter","url":"https://provider.smoke.test/inbound","enabled":false}'
[[ "$CODE" == "200" ]] || fail "Hosted-adapter disable failed: $CODE $(cat "$RESP_BODY")"
request GET "$planner/_internal/gateway/email/hosted-adapters/smoke.adapter" -H "$internal"
[[ "$CODE" == "404" ]] || fail "Disabled hosted adapter was still resolvable: $CODE"

# Mailbox account and connection: HTTPS control plane, health-gated enable, and
# unavailable credential resolution while disabled.
request POST "$planner/accounts" -H "$auth" -H 'X-Forwarded-Proto: https' \
    -H 'Content-Type: application/json' -d '{"id":"smoke"}'
[[ "$CODE" == "201" ]] || fail "Mailbox account creation failed: $CODE $(cat "$RESP_BODY")"
account_token="$(field token)"
[[ "$account_token" =~ ^[a-f0-9]{64}$ ]] || fail "Mailbox account token was not issued once."
tenant_auth="Authorization: Bearer $account_token"

request POST "$planner/email/mailbox-accounts/smoke/connections" -H "$tenant_auth" -H 'X-Forwarded-Proto: https' \
    -H 'Content-Type: application/json' \
    -d '{"address":"user@managed.test","imap_upstream_id":"mail","smtp_upstream_id":"mail","imap_username":"user@managed.test","smtp_username":"user@managed.test","imap_auth":"password","smtp_auth":"password","imap_secret":"imap-secret","smtp_secret":"smtp-secret"}'
[[ "$CODE" == "201" ]] || fail "Mailbox connection creation failed: $CODE $(cat "$RESP_BODY")"
connection_id="$(field connection id)"
[[ "$(field connection enabled)" == "false" && "$(field connection health)" == "untested" ]] \
    || fail "Mailbox connection was not created in a disabled, untested state."

request PATCH "$planner/email/mailbox-accounts/smoke/connections/$connection_id" -H "$tenant_auth" -H 'X-Forwarded-Proto: https' \
    -H 'Content-Type: application/json' -d '{"enabled":true}'
[[ "$CODE" == "409" ]] || fail "Mailbox connection was enabled without a health test: $CODE"

request POST "$planner/email/mailbox-accounts/smoke/connections/$connection_id/test" -H "$tenant_auth" -H 'X-Forwarded-Proto: https'
[[ "$CODE" == "200" && "$(field health)" == "unhealthy" ]] \
    || fail "Unreachable mailbox provider was not recorded unhealthy: $CODE $(cat "$RESP_BODY")"
request PATCH "$planner/email/mailbox-accounts/smoke/connections/$connection_id" -H "$tenant_auth" -H 'X-Forwarded-Proto: https' \
    -H 'Content-Type: application/json' -d '{"enabled":true}'
[[ "$CODE" == "409" ]] || fail "Unhealthy mailbox connection was enabled: $CODE"

request GET "$planner/_internal/gateway/mailbox-connections/$connection_id/smtp" -H "$internal"
[[ "$CODE" == "404" ]] || fail "Disabled mailbox credential was resolvable: $CODE"

request DELETE "$planner/email/mailbox-accounts/smoke/connections/$connection_id" -H "$tenant_auth" -H 'X-Forwarded-Proto: https'
[[ "$CODE" == "200" ]] || fail "Mailbox connection revocation failed: $CODE"

# Secrets stay encrypted at rest: no plaintext key, token, or mailbox secret in
# the shared resource collection or its revision history.
php -r '
    $db = new PDO("sqlite:".$argv[1]);
    $documents = "";
    foreach ($db->query("SELECT document FROM configurations") as $row) { $documents .= (string) $row["document"]; }
    foreach ($db->query("SELECT document FROM configuration_revisions") as $row) { $documents .= (string) $row["document"]; }
    foreach (["PRIVATE KEY", "tok-one", "tok-two", "imap-secret", "smtp-secret", "notify-one", "notify-two"] as $needle) {
        if (str_contains($documents, $needle)) {
            fwrite(STDERR, "Configuration document leaked plaintext secret: {$needle}\n"); exit(1);
        }
    }
    $statement = $db->prepare("SELECT COUNT(*) FROM configurations WHERE id = ?");
    $statement->execute([$argv[2]]);
    if ((int) $statement->fetchColumn() !== 0) { fwrite(STDERR, "Mailbox resource was not revoked.\n"); exit(1); }
    $statement = $db->prepare("SELECT COUNT(*) FROM configuration_secrets WHERE resource_id = ?");
    $statement->execute([$argv[2]]);
    if ((int) $statement->fetchColumn() !== 0) { fwrite(STDERR, "Mailbox credentials were not revoked.\n"); exit(1); }
' "$DB_FILE" "$connection_id"

echo "Configuration-ownership smoke passed: Shell grants, Webstream private-source/route-policy, schedule notification, and Mail resolution across two Spinners."
