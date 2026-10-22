#!/usr/bin/env bash

# Disposable end-to-end Shell-over-SSH verification.
#
# This starts a throwaway OpenSSH server on loopback whose account is forced to
# run the real gateway-shell-helper, a Planner PHP fixture, and one Spinner
# built with the gateway_shell extension. It then exercises the public Planner
# subscription API against the private Planner credential resolution and the
# live Spinner execution path: grant, create, execute, rotate, revoke, host-key
# mismatch, and an unreachable destination.
#
# It never touches a production host and it does not need root: sshd runs as
# the invoking user on a high loopback port.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_PATH="${GATEWAY_SHELL_APP_PATH:-$ROOT/tests/Fixtures/octane-app}"
PHP_PORT="${GATEWAY_SHELL_PHP_PORT:-18261}"
HTTP_PORT="${GATEWAY_SHELL_HTTP_PORT:-18262}"
SSHD_PORT="${GATEWAY_SHELL_SSHD_PORT:-22927}"
SSHD_BIN="${GATEWAY_SHELL_SSHD_BIN:-$(command -v sshd || echo /usr/sbin/sshd)}"
TOKEN="${GATEWAY_INTERNAL_TOKEN:-shell-ssh-smoke-token}"
REGISTRY_TOKEN="${REGISTRY_TOKEN:-shell-ssh-registry-token}"
SHELL_TOKEN="${GATEWAY_SHELL_SUBSCRIPTION_TOKEN:-shell-ssh-subscription-token}"
TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/gateway-shell-ssh.XXXXXX")"
DB_FILE="$TEMP_DIR/gateway.sqlite"
PHP_LOG="$TEMP_DIR/php.log"
HTTP_LOG="$TEMP_DIR/spinner.log"
SSHD_LOG="$TEMP_DIR/sshd.log"
GATEWAY_BIN="$TEMP_DIR/gateway"
HELPER_BIN="$TEMP_DIR/gateway-shell-helper"
HOOKS_DIR="$TEMP_DIR/hooks"
HOST_KEY="$TEMP_DIR/host_ed25519"
CLIENT_KEY="$TEMP_DIR/client_ed25519"
CLIENT2_KEY="$TEMP_DIR/client2_ed25519"
PHP_PID=""
HTTP_PID=""
SSHD_PID=""

cleanup() {
    local status=$?
    for pid in "$HTTP_PID" "$SSHD_PID" "$PHP_PID"; do
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
        fi
    done
    if [[ "$status" -ne 0 ]]; then
        echo "Shell SSH smoke diagnostics preserved at $TEMP_DIR" >&2
        return
    fi
    case "$TEMP_DIR" in
        "${TMPDIR:-/tmp}"/gateway-shell-ssh.*) rm -r -- "$TEMP_DIR" ;;
    esac
}
trap cleanup EXIT INT TERM

if [[ ! -f "$APP_PATH/bootstrap/app.php" || ! -f "$ROOT/vendor/autoload.php" ]]; then
    echo 'Laravel Shell fixture and Composer dependencies are required.' >&2
    exit 1
fi
if [[ ! -x "$SSHD_BIN" ]]; then
    echo "OpenSSH sshd is required (looked for $SSHD_BIN)." >&2
    exit 1
fi
for tool in ssh ssh-keygen curl php; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        echo "$tool is required for the Shell SSH smoke." >&2
        exit 1
    fi
done

touch "$DB_FILE"
GO_BUILD_FLAGS="${GOFLAGS:--mod=mod}"
go build -tags gateway_shell "$GO_BUILD_FLAGS" -o "$GATEWAY_BIN" "$ROOT/src/spinner/cmd/proxy"
go build "$GO_BUILD_FLAGS" -o "$HELPER_BIN" "$ROOT/ext/shell/cmd/gateway-shell-helper"

# --- Disposable forced-command OpenSSH server ------------------------------
ssh-keygen -q -t ed25519 -N '' -f "$HOST_KEY"
ssh-keygen -q -t ed25519 -N '' -f "$CLIENT_KEY"
ssh-keygen -q -t ed25519 -N '' -f "$CLIENT2_KEY"
HOST_PUB="$(awk '{print $1" "$2}' "$HOST_KEY.pub")"

mkdir -p "$HOOKS_DIR/github"
cat > "$HOOKS_DIR/github/deploy" <<'HOOK'
#!/bin/sh
cat >/dev/null
printf '%s' '{"response":{"status":202,"body":"deploy-ok"}}'
HOOK
chmod 0755 "$HOOKS_DIR/github/deploy"

cat > "$TEMP_DIR/helper-wrapper.sh" <<WRAPPER
#!/bin/sh
export GATEWAY_SHELL_HOOKS_DIR="$HOOKS_DIR"
export GATEWAY_SHELL_OUTPUT_MODE=outcome
exec "$HELPER_BIN"
WRAPPER
chmod 0755 "$TEMP_DIR/helper-wrapper.sh"

cat > "$TEMP_DIR/authorized_keys" <<AUTH
restrict,command="$TEMP_DIR/helper-wrapper.sh" $(cat "$CLIENT_KEY.pub")
AUTH
chmod 0600 "$TEMP_DIR/authorized_keys"

cat > "$TEMP_DIR/sshd_config" <<SSHD
Port $SSHD_PORT
ListenAddress 127.0.0.1
HostKey $HOST_KEY
PidFile $TEMP_DIR/sshd.pid
AuthorizedKeysFile $TEMP_DIR/authorized_keys
PasswordAuthentication no
KbdInteractiveAuthentication no
UsePAM no
StrictModes no
PermitUserEnvironment no
AllowTcpForwarding no
AllowAgentForwarding no
X11Forwarding no
LogLevel VERBOSE
SSHD
"$SSHD_BIN" -D -f "$TEMP_DIR/sshd_config" -E "$SSHD_LOG" &
SSHD_PID=$!
for _ in $(seq 1 30); do
    if ssh -i "$CLIENT_KEY" -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no \
        -o IdentitiesOnly=yes -o BatchMode=yes -p "$SSHD_PORT" 127.0.0.1 true >/dev/null 2>&1; then
        break
    fi
    if ! kill -0 "$SSHD_PID" 2>/dev/null; then
        tail -40 "$SSHD_LOG" >&2
        exit 1
    fi
    sleep 0.1
done
CURRENT_USER="$(id -un)"

# --- Planner and Spinner ----------------------------------------------------
(
    cd "$APP_PATH"
    APP_ENV=testing APP_KEY="base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" \
    COMPOSER_VENDOR_DIR="$ROOT/vendor" DB_CONNECTION=sqlite DB_DATABASE="$DB_FILE" \
    GATEWAY_HTTP_REGISTRY_SMOKE=1 \
    GATEWAY_PLANNER='Webong\Gateway\Tests\Fixtures\HttpSmokePlanner' \
    GATEWAY_PATH_RESOLVER='Webong\Gateway\Tests\Fixtures\HttpSmokePathResolver' \
    GATEWAY_INTERNAL_TOKEN="$TOKEN" REGISTRY_TOKEN="$REGISTRY_TOKEN" \
    GATEWAY_SHELL_SUBSCRIPTION_TOKEN="$SHELL_TOKEN" \
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

GATEWAY_RUNTIME=http GATEWAY_PLANNER_URL="http://127.0.0.1:$PHP_PORT" \
GATEWAY_INTERNAL_TOKEN="$TOKEN" PORT="$HTTP_PORT" LOG_LEVEL=error \
GATEWAY_SHELL_ENABLED=true GATEWAY_SHELL_SSH_ALLOWED_CIDRS=127.0.0.0/8 \
GATEWAY_SHELL_TIMEOUT=10s \
"$GATEWAY_BIN" >"$HTTP_LOG" 2>&1 &
HTTP_PID=$!
wait_http "http://127.0.0.1:$HTTP_PORT/health" "$HTTP_LOG" "$HTTP_PID"

planner="http://127.0.0.1:$PHP_PORT"
edge="http://127.0.0.1:$HTTP_PORT"
auth="Authorization: Bearer $REGISTRY_TOKEN"
shell_auth="X-Gateway-Shell-Subscription: $SHELL_TOKEN"
endpoint_key="http-smoke-endpoint"

json_field() {
    php -r '$v=json_decode(file_get_contents($argv[1]), true, 512, JSON_THROW_ON_ERROR); echo $v[$argv[2]] ?? "";' "$1" "$2"
}
status_of() {
    awk '/^HTTP\// { code = $2 } END { print code }' "$1"
}
header_of() {
    awk -v name="$2" 'tolower($1) == tolower(name":") { sub(/\r$/, "", $2); print $2; exit }' "$1"
}

curl -fsS -X POST "$edge/registry/endpoints" -H "$auth" -H 'Content-Type: application/json' \
    -d "{\"client\":\"http-smoke\",\"external_id\":\"shell-ssh-provider\",\"endpoint_key\":\"$endpoint_key\",\"managed\":true}" >/dev/null

# An SSH grant pins the exact endpoint, subscriber, hook, and host identity.
curl -fsS -X POST "$planner/shell/hook-grants" -H "$auth" -H "$shell_auth" -H 'Content-Type: application/json' \
    -d "{\"endpoint_key\":\"$endpoint_key\",\"subscriber_id\":\"deploy-owner\",\"hook\":\"github/deploy\",\"mode\":\"ssh\",\"host\":\"127.0.0.1\",\"port\":$SSHD_PORT,\"user\":\"$CURRENT_USER\",\"host_public_key\":\"$HOST_PUB\"}" >/dev/null

private_key="$(cat "$CLIENT_KEY")"
create_subscription() {
    local id="$1" hook="$2" host="$3" port="$4" user="$5" pin="$6" key="$7"
    local body
    body="$(php -r '
        $args = array_slice($argv, 1);
        echo json_encode([
            "subscriber_id" => $args[1], "subscription_id" => $args[0], "type" => "reply",
            "target_kind" => "shell", "hook" => $args[2],
            "webhook_group" => "shell-smoke", "routing_scope" => "application", "routing_key" => "app-123",
            "ssh" => ["host" => $args[3], "port" => (int) $args[4], "user" => $args[5],
                "host_public_key" => $args[6], "private_key" => $args[7]],
        ], JSON_THROW_ON_ERROR);
    ' "$id" "$id-owner" "$hook" "$host" "$port" "$user" "$pin" "$key")"
    curl -sS -D "$TEMP_DIR/sub.headers" -o "$TEMP_DIR/sub.body" -X POST \
        "$edge/registry/endpoints/$endpoint_key/subscriptions" \
        -H "$auth" -H "$shell_auth" -H 'Content-Type: application/json' -d "$body"
}

ingress() {
    curl -sS -D "$TEMP_DIR/ingress.headers" -o "$TEMP_DIR/ingress.body" -w '%{http_code}' \
        -X POST -H 'Content-Type: application/json' --data '{"event":"push"}' \
        "$edge/http-smoke/registry-ingress"
}

# 1. Create the SSH reply subscription through the public registry API.
create_subscription "deploy" "github/deploy" "127.0.0.1" "$SSHD_PORT" "$CURRENT_USER" "$HOST_PUB" "$private_key"
[[ "$(status_of "$TEMP_DIR/sub.headers")" == "201" ]]
sub_id="$(json_field "$TEMP_DIR/sub.body" id)"
etag="$(header_of "$TEMP_DIR/sub.headers" ETag)"
[[ -n "$sub_id" && -n "$etag" ]]
# Public reads must expose the pinned identity but never the private key.
if grep -q 'PRIVATE KEY' "$TEMP_DIR/sub.body"; then
    echo 'Subscription read leaked SSH private key material.' >&2
    exit 1
fi
[[ "$(php -r '$v=json_decode(file_get_contents($argv[1]), true, 512, JSON_THROW_ON_ERROR); echo ($v["ssh"]["host_public_key"] ?? "")."|".($v["ssh"]["credential_version"] ?? "");' "$TEMP_DIR/sub.body")" == "$HOST_PUB|1" ]]

# 2. A request reaches the remote hook through the real OpenSSH helper.
code="$(ingress)"
if [[ "$code" != "202" || "$(<"$TEMP_DIR/ingress.body")" != "deploy-ok" ]]; then
    echo "SSH Shell execution failed: status=$code body=$(<"$TEMP_DIR/ingress.body")" >&2
    cat "$HTTP_LOG" "$PHP_LOG" >&2
    exit 1
fi
run_id="$(header_of "$TEMP_DIR/ingress.headers" X-Gateway-Shell-Run-Id)"
[[ "$run_id" =~ ^[a-f0-9]{32}$ ]]

# 3. Rotate the client key. The new key works; the old key is revoked.
create_etag="$etag"
curl -sS -D "$TEMP_DIR/rotate.headers" -o "$TEMP_DIR/rotate.body" -X POST \
    "$edge/registry/endpoints/$endpoint_key/subscriptions/$sub_id/ssh/rotate-key" \
    -H "$auth" -H "$shell_auth" -H "If-Match: $create_etag" -H 'Content-Type: application/json' \
    -d "$(php -r '$key=file_get_contents($argv[1]); echo json_encode(["private_key"=>$key], JSON_THROW_ON_ERROR);' "$CLIENT2_KEY")"
[[ "$(status_of "$TEMP_DIR/rotate.headers")" == "200" ]]
etag="$(header_of "$TEMP_DIR/rotate.headers" ETag)"
[[ -n "$etag" && "$etag" != "$create_etag" ]]
code="$(ingress)"
[[ "$code" == "202" && "$(<"$TEMP_DIR/ingress.body")" == "deploy-ok" ]]
if ssh -i "$CLIENT_KEY" -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no \
    -o IdentitiesOnly=yes -o BatchMode=yes -p "$SSHD_PORT" 127.0.0.1 true >/dev/null 2>&1; then
    echo 'Rotated-away SSH client key still authenticated.' >&2
    exit 1
fi

# The live credential is encrypted and revision history never holds the key.
php -r '
    $db = new PDO("sqlite:".$argv[1]);
    foreach ($db->query("SELECT document FROM configuration_revisions") as $row) {
        if (str_contains((string) $row["document"], "PRIVATE KEY")) {
            fwrite(STDERR, "Configuration revision history leaked a private key.\n"); exit(1);
        }
    }
    $secret = $db->query("SELECT ciphertext FROM configuration_secrets")->fetchColumn();
    if ($secret === false || str_contains((string) $secret, "PRIVATE KEY")) {
        fwrite(STDERR, "Live SSH credential was not stored encrypted.\n"); exit(1);
    }
' "$DB_FILE"

# A stale If-Match on rotation must be rejected and must not corrupt live state.
stale_status="$(curl -sS -o /dev/null -w '%{http_code}' -X POST \
    "$edge/registry/endpoints/$endpoint_key/subscriptions/$sub_id/ssh/rotate-key" \
    -H "$auth" -H "$shell_auth" -H "If-Match: $create_etag" -H 'Content-Type: application/json' \
    -d "$(php -r '$key=file_get_contents($argv[1]); echo json_encode(["private_key"=>$key], JSON_THROW_ON_ERROR);' "$CLIENT_KEY")")"
[[ "$stale_status" == "412" ]]

# A stale revision must not have disturbed the rotated credential.
code="$(ingress)"
[[ "$code" == "202" && "$(<"$TEMP_DIR/ingress.body")" == "deploy-ok" ]]

# 4. Remove the subscription and confirm the live credential is revoked.
delete_status="$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE \
    "$edge/registry/endpoints/$endpoint_key/subscriptions/$sub_id" \
    -H "$auth" -H "$shell_auth" -H "If-Match: $etag")"
[[ "$delete_status" == "204" ]]
code="$(ingress)"
if [[ "$code" == "202" && "$(<"$TEMP_DIR/ingress.body")" == "deploy-ok" ]]; then
    echo "Revoked SSH Shell subscription was still executed: status=$code body=$(<"$TEMP_DIR/ingress.body")" >&2
    exit 1
fi
php -r '
    $db = new PDO("sqlite:".$argv[1]);
    $stmt = $db->prepare("SELECT COUNT(*) FROM configurations WHERE kind = ? AND enabled = 1");
    $stmt->execute(["shell.ssh-execution"]);
    $enabled = (int) $stmt->fetchColumn();
    $secrets = (int) $db->query("SELECT COUNT(*) FROM configuration_secrets")->fetchColumn();
    if ($enabled !== 0 || $secrets !== 0) {
        fwrite(STDERR, "Removal did not revoke the live SSH credential (enabled=$enabled secrets=$secrets).\n"); exit(1);
    }
' "$DB_FILE"

# 5. Host-key mismatch fails closed: pin a different host identity.
ssh-keygen -q -t ed25519 -N '' -f "$TEMP_DIR/wrong_host_ed25519"
wrong_pub="$(awk '{print $1" "$2}' "$TEMP_DIR/wrong_host_ed25519.pub")"
curl -fsS -X POST "$planner/shell/hook-grants" -H "$auth" -H "$shell_auth" -H 'Content-Type: application/json' \
    -d "{\"endpoint_key\":\"$endpoint_key\",\"subscriber_id\":\"mismatch-owner\",\"hook\":\"github/deploy\",\"mode\":\"ssh\",\"host\":\"127.0.0.1\",\"port\":$SSHD_PORT,\"user\":\"$CURRENT_USER\",\"host_public_key\":\"$wrong_pub\"}" >/dev/null
create_subscription "mismatch" "github/deploy" "127.0.0.1" "$SSHD_PORT" "$CURRENT_USER" "$wrong_pub" "$(cat "$CLIENT_KEY")"
[[ "$(status_of "$TEMP_DIR/sub.headers")" == "201" ]]
mismatch_id="$(json_field "$TEMP_DIR/sub.body" id)"
mismatch_etag="$(header_of "$TEMP_DIR/sub.headers" ETag)"
code="$(ingress)"
if [[ "$code" != "502" ]]; then
    echo "Pinned host-key mismatch was accepted: status=$code body=$(<"$TEMP_DIR/ingress.body")" >&2
    exit 1
fi
# Reaching the SSH execution stage proves the plan resolved; the handshake is
# what fails when the pinned identity does not match the presented one.
[[ "$(header_of "$TEMP_DIR/ingress.headers" X-Gateway-Shell-Run-Id)" =~ ^[a-f0-9]{32}$ ]]
curl -sS -o /dev/null -X DELETE "$edge/registry/endpoints/$endpoint_key/subscriptions/$mismatch_id" \
    -H "$auth" -H "$shell_auth" -H "If-Match: $mismatch_etag"

# 6. An unreachable (but ceiling-allowed) destination must fail closed.
closed_port="${GATEWAY_SHELL_CLOSED_PORT:-9}"
curl -fsS -X POST "$planner/shell/hook-grants" -H "$auth" -H "$shell_auth" -H 'Content-Type: application/json' \
    -d "{\"endpoint_key\":\"$endpoint_key\",\"subscriber_id\":\"down-owner\",\"hook\":\"github/deploy\",\"mode\":\"ssh\",\"host\":\"127.0.0.1\",\"port\":$closed_port,\"user\":\"$CURRENT_USER\",\"host_public_key\":\"$HOST_PUB\"}" >/dev/null
create_subscription "down" "github/deploy" "127.0.0.1" "$closed_port" "$CURRENT_USER" "$HOST_PUB" "$(cat "$CLIENT_KEY")"
[[ "$(status_of "$TEMP_DIR/sub.headers")" == "201" ]]
down_id="$(json_field "$TEMP_DIR/sub.body" id)"
down_etag="$(header_of "$TEMP_DIR/sub.headers" ETag)"
code="$(ingress)"
if [[ "$code" != "502" ]]; then
    echo "Unreachable SSH destination did not fail closed: status=$code body=$(<"$TEMP_DIR/ingress.body")" >&2
    exit 1
fi
curl -sS -o /dev/null -X DELETE "$edge/registry/endpoints/$endpoint_key/subscriptions/$down_id" \
    -H "$auth" -H "$shell_auth" -H "If-Match: $down_etag"

echo "Shell SSH forced-command integration smoke passed: create, execute, rotate, revoke, host-key mismatch, unreachable host."
