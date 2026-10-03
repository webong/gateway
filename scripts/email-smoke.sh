#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_PATH="${GATEWAY_EMAIL_APP_PATH:-$ROOT/tests/Fixtures/octane-app}"
PHP_PORT="${GATEWAY_EMAIL_PHP_PORT:-18191}"
GO_PORT="${GATEWAY_EMAIL_GO_PORT:-18192}"
SMTP_PORT="${GATEWAY_EMAIL_SMTP_PORT:-12528}"
PHP2_PORT="${GATEWAY_EMAIL_PHP2_PORT:-18193}"
GO2_PORT="${GATEWAY_EMAIL_GO2_PORT:-18194}"
SMTP2_PORT="${GATEWAY_EMAIL_SMTP2_PORT:-12529}"
RELAY_PORT="${GATEWAY_EMAIL_RELAY_PORT:-12527}"
DNS_PORT="${GATEWAY_EMAIL_DNS_PORT:-12530}"
TOKEN="${GATEWAY_INTERNAL_TOKEN:-email-smoke-token}"
REGISTRY_TOKEN="${REGISTRY_TOKEN:-email-registry-smoke-token}"
SRS_KEY="$(php -r 'echo base64_encode(str_repeat("s", 32));')"
TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/gateway-email.XXXXXX")"
DB_FILE="$TEMP_DIR/gateway.sqlite"
RELAY_FILE="$TEMP_DIR/accepted.jsonl"
PHP_LOG="$TEMP_DIR/php.log"
GO_LOG="$TEMP_DIR/spinner.log"
PHP2_LOG="$TEMP_DIR/php-2.log"
GO2_LOG="$TEMP_DIR/spinner-2.log"
RELAY_LOG="$TEMP_DIR/relay.log"
GO_BIN="$TEMP_DIR/gateway"
CLIENT_BIN="$TEMP_DIR/smtp-client"
RELAY_BIN="$TEMP_DIR/smtp-relay"
PHP_PID=""
GO_PID=""
PHP2_PID=""
GO2_PID=""
RELAY_PID=""

cleanup() {
    local status=$?
    for pid in "$GO2_PID" "$PHP2_PID" "$GO_PID" "$RELAY_PID" "$PHP_PID"; do
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
        fi
    done
    if [[ "$status" -ne 0 ]]; then
        echo "Email smoke diagnostics preserved at $TEMP_DIR" >&2
        return
    fi
    case "$TEMP_DIR" in
        "${TMPDIR:-/tmp}"/gateway-email.*) rm -r -- "$TEMP_DIR" ;;
    esac
}
trap cleanup EXIT INT TERM

if [[ ! -f "$APP_PATH/bootstrap/app.php" || ! -x "$ROOT/vendor/bin/roadrunner-worker" ]]; then
    echo 'Laravel fixture and Composer dependencies are required.' >&2
    exit 1
fi

touch "$DB_FILE"
GO_BUILD_FLAGS="${GOFLAGS:--mod=mod}"
go build "$GO_BUILD_FLAGS" -o "$GO_BIN" "$ROOT/src/spinner/cmd/proxy"
go build "$GO_BUILD_FLAGS" -o "$CLIENT_BIN" "$ROOT/src/spinner/cmd/smtp-smoke"
go build "$GO_BUILD_FLAGS" -o "$RELAY_BIN" "$ROOT/src/spinner/cmd/email-relay-smoke"

(
    cd "$APP_PATH"
    APP_ENV=testing \
    APP_KEY="base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" \
    COMPOSER_VENDOR_DIR="$ROOT/vendor" \
    GATEWAY_INTERNAL_TOKEN="$TOKEN" \
    REGISTRY_TOKEN="$REGISTRY_TOKEN" \
    GATEWAY_PATH_RESOLVER='Webong\Gateway\Tests\Fixtures\HttpSmokePathResolver' \
    GATEWAY_HTTP_REGISTRY_SMOKE=1 \
    GATEWAY_EMAIL_SMOKE=1 \
    GATEWAY_EMAIL_POLICY='Webong\Gateway\Tests\Fixtures\EmailSmokeMessagePolicy' \
    GATEWAY_EMAIL_DMARC_ENFORCE_ENABLED=true \
    GATEWAY_EMAIL_VERIFICATION_TTL_SECONDS=0 \
    GATEWAY_EMAIL_SRS_ENABLED=true \
    GATEWAY_EMAIL_SRS_DOMAIN=managed.test \
    GATEWAY_EMAIL_SRS_KEYS="[\"$SRS_KEY\"]" \
    DB_CONNECTION=sqlite DB_DATABASE="$DB_FILE" \
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
wait_tcp() {
    local port="$1" log="$2" pid="$3"
    for _ in $(seq 1 60); do
        if (echo >/dev/tcp/127.0.0.1/"$port") >/dev/null 2>&1; then return 0; fi
        if ! kill -0 "$pid" 2>/dev/null; then break; fi
        sleep 0.25
    done
    tail -80 "$log" >&2
    return 1
}

wait_http "http://127.0.0.1:$PHP_PORT/http-smoke/worker" "$PHP_LOG" "$PHP_PID"
"$RELAY_BIN" -addr "127.0.0.1:$RELAY_PORT" -dns-addr "127.0.0.1:$DNS_PORT" -output "$RELAY_FILE" >"$RELAY_LOG" 2>&1 &
RELAY_PID=$!
wait_tcp "$RELAY_PORT" "$RELAY_LOG" "$RELAY_PID"

GATEWAY_RUNTIME=http \
GATEWAY_LARAVEL_BACKEND_URL="http://127.0.0.1:$PHP_PORT" \
GATEWAY_INTERNAL_TOKEN="$TOKEN" \
GATEWAY_SMTP_ADDR="127.0.0.1:$SMTP_PORT" \
GATEWAY_SMTP_HOSTNAME='smtp.managed.test' \
GATEWAY_SMTP_RELAY_ADDR="127.0.0.1:$RELAY_PORT" \
GATEWAY_SMTP_RELAY_TLS_MODE=none \
GATEWAY_EMAIL_DOMAIN_RESOLVER_ADDR="127.0.0.1:$DNS_PORT" \
GATEWAY_EMAIL_QUARANTINE_PATH="$TEMP_DIR/quarantine" \
GATEWAY_DELIVERY_SPOOL_PATH="$TEMP_DIR/spool" \
PORT="$GO_PORT" \
"$GO_BIN" >"$GO_LOG" 2>&1 &
GO_PID=$!
wait_http "http://127.0.0.1:$GO_PORT/health" "$GO_LOG" "$GO_PID"

planner="http://127.0.0.1:$PHP_PORT"
auth="Authorization: Bearer $REGISTRY_TOKEN"
domain_json="$(curl -fsS -X POST "$planner/email/domains" -H "$auth" -H 'Content-Type: application/json' -d '{"domain":"managed.test"}')"
domain_id="$(printf '%s' "$domain_json" | php -r '$v=json_decode(stream_get_contents(STDIN),true); echo $v["id"] ?? "";')"
if [[ -z "$domain_id" ]]; then echo 'Email domain creation returned no ID.' >&2; exit 1; fi
curl -fsS -X POST "$planner/email/domains/$domain_id/verify" -H "$auth" >/dev/null
curl -fsS -X PATCH "$planner/email/domains/$domain_id" -H "$auth" -H 'Content-Type: application/json' -d '{"enabled":true}' >/dev/null
aliases="$planner/email/domains/$domain_id/aliases"
curl -fsS -X POST "$aliases" -H "$auth" -H 'Content-Type: application/json' -d '{"local_part":"first","recipients":["one@example.net","shared@example.net"]}' >/dev/null
curl -fsS -X POST "$aliases" -H "$auth" -H 'Content-Type: application/json' -d '{"local_part":"second","recipients":["two@example.net","shared@example.net"]}' >/dev/null
curl -fsS -X POST "$aliases" -H "$auth" -H 'Content-Type: application/json' -d '{"local_part":"reject","recipients":["refuse@example.net"]}' >/dev/null

"$CLIENT_BIN" -addr "127.0.0.1:$SMTP_PORT" -to 'first@managed.test,second@managed.test' -subject 'gateway email smoke'
for _ in $(seq 1 40); do
    if [[ -s "$RELAY_FILE" ]]; then break; fi
    sleep 0.1
done
php -r '
    $rows = file($argv[1], FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES);
    $mail = is_array($rows) && count($rows) === 1 ? json_decode($rows[0], true) : null;
    if (!is_array($mail) || !preg_match("/^SRS0-[A-Za-z0-9_-]+@managed\\.test$/", $mail["from"] ?? "") ||
        $mail["recipients"] !== ["one@example.net", "shared@example.net", "two@example.net"] ||
        !str_contains($mail["data"], "Subject: gateway email smoke")) {
        fwrite(STDERR, "Relay did not accept one complete managed-alias envelope.\n"); exit(1);
    }
' "$RELAY_FILE"

srs_address="$(php -r '
    $row = json_decode((string) file($argv[1], FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES)[0], true);
    echo $row["from"] ?? "";
' "$RELAY_FILE")"

suppression_json="$(curl -fsS -X POST "$planner/email/suppressions" -H "$auth" -H 'Content-Type: application/json' -d '{"address":"shared@example.net","reason":"hard_bounce"}')"
suppression_id="$(printf '%s' "$suppression_json" | php -r '$v=json_decode(stream_get_contents(STDIN),true); echo $v["id"] ?? "";')"
if [[ ! "$suppression_id" =~ ^[a-f0-9]{64}$ ]]; then echo 'Email suppression creation returned no valid ID.' >&2; exit 1; fi
"$CLIENT_BIN" -addr "127.0.0.1:$SMTP_PORT" -to 'first@managed.test' -subject 'suppressed destination' -expect-data-error -expect-data-code 550
curl -fsS -X DELETE "$planner/email/suppressions/$suppression_id" -H "$auth" >/dev/null

"$CLIENT_BIN" -addr "127.0.0.1:$SMTP_PORT" -to 'reject@managed.test' -expect-data-error -expect-data-code 451
"$CLIENT_BIN" -addr "127.0.0.1:$SMTP_PORT" -to 'first@managed.test' -subject 'policy-block' -expect-data-error -expect-data-code 550
"$CLIENT_BIN" -addr "127.0.0.1:$SMTP_PORT" -to 'first@managed.test' -subject 'policy-defer' -expect-data-error -expect-data-code 451
"$CLIENT_BIN" -addr "127.0.0.1:$SMTP_PORT" -to 'first@managed.test' -subject 'policy-error' -expect-data-error -expect-data-code 451
"$CLIENT_BIN" -addr "127.0.0.1:$SMTP_PORT" -to 'unknown@managed.test' -expect-rcpt-code 550
"$CLIENT_BIN" -addr "127.0.0.1:$SMTP_PORT" -to 'first@managed.test,other@unmanaged.test' -expect-rcpt-code 550
if [[ "$(wc -l < "$RELAY_FILE")" -ne 1 ]]; then
    echo 'Rejected messages reached the outbound relay.' >&2
    exit 1
fi

curl -fsS -X POST "$aliases" -H "$auth" -H 'Content-Type: application/json' -d '{"local_part":"*","recipients":["fallback@example.net"]}' >/dev/null
curl -fsS -X POST "$aliases" -H "$auth" -H 'Content-Type: application/json' -d '{"local_part":"team*","recipients":["team@example.net"]}' >/dev/null
"$CLIENT_BIN" -addr "127.0.0.1:$SMTP_PORT" -to 'unknown@managed.test' -subject 'gateway catch-all smoke'
"$CLIENT_BIN" -addr "127.0.0.1:$SMTP_PORT" -to 'first@managed.test' -subject 'gateway exact-over-catch-all smoke'
"$CLIENT_BIN" -addr "127.0.0.1:$SMTP_PORT" -to 'team-payments@managed.test' -subject 'gateway pattern smoke'
php -r '
    $rows = file($argv[1], FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES);
    $fallback = is_array($rows) && count($rows) === 4 ? json_decode($rows[1], true) : null;
    $exact = is_array($rows) && count($rows) === 4 ? json_decode($rows[2], true) : null;
    $pattern = is_array($rows) && count($rows) === 4 ? json_decode($rows[3], true) : null;
    if (!is_array($fallback) || $fallback["recipients"] !== ["fallback@example.net"] ||
        !is_array($exact) || $exact["recipients"] !== ["one@example.net", "shared@example.net"] ||
        !is_array($pattern) || $pattern["recipients"] !== ["team@example.net"]) {
        fwrite(STDERR, "Catch-all, pattern, or exact-alias precedence failed.\n"); exit(1);
    }
' "$RELAY_FILE"

relay_message() {
    php -r '
        $rows = file($argv[1], FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES);
        $mail = is_array($rows) ? json_decode($rows[(int) $argv[2]] ?? "", true) : null;
        if (!is_array($mail) || !is_string($mail["data"] ?? null)) exit(1);
        fwrite(STDOUT, $mail["data"]);
    ' "$RELAY_FILE" "$1"
}
relay_message 0 | "$CLIENT_BIN" -addr "127.0.0.1:$SMTP_PORT" -to 'first@managed.test' -message-file -
relay_message 4 | "$CLIENT_BIN" -addr "127.0.0.1:$SMTP_PORT" -to 'first@managed.test' -message-file -
relay_message 5 | "$CLIENT_BIN" -addr "127.0.0.1:$SMTP_PORT" -to 'first@managed.test' -message-file - -expect-data-error -expect-data-code 554
php -r '
    $rows = file($argv[1], FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES);
    $last = is_array($rows) && count($rows) === 6 ? json_decode($rows[5], true) : null;
    if (!is_array($last) || substr_count($last["data"] ?? "", "X-Gateway-Forwarded:") !== 3) {
        fwrite(STDERR, "Signed forwarding loop was not stopped before a fourth relay.\n"); exit(1);
    }
' "$RELAY_FILE"

# The relay persists this delivery, then drops the connection before sending
# its DATA acknowledgment. Spinner must report uncertainty, while Planner's
# SRS mapping remains available to reverse a later bounce.
"$CLIENT_BIN" -addr "127.0.0.1:$SMTP_PORT" -to 'first@managed.test' -subject 'gateway-smoke-drop-ack' -expect-data-error -expect-data-code 451
uncertain_srs_address="$(php -r '
    $rows = file($argv[1], FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES);
    $mail = is_array($rows) && count($rows) === 7 ? json_decode($rows[6], true) : null;
    if (!is_array($mail) || !preg_match("/^SRS0-[A-Za-z0-9_-]+@managed\\.test$/", $mail["from"] ?? "")) {
        fwrite(STDERR, "Uncertain relay acceptance lost its SRS return path.\n"); exit(1);
    }
    echo $mail["from"];
' "$RELAY_FILE")"

printf 'From: Sender <sender@quarantine.example.test>\r\nSubject: gateway DMARC quarantine smoke\r\n\r\nquarantine me\r\n' |
    "$CLIENT_BIN" -addr "127.0.0.1:$SMTP_PORT" -to 'first@managed.test' -message-file -
php -r '
    $files = glob($argv[1]."/*.quarantine");
    $rows = file($argv[2], FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES);
    $stored = count($files) === 1 ? file_get_contents($files[0]) : false;
    if (!is_string($stored) || !str_contains($stored, "Subject: gateway DMARC quarantine smoke") ||
        !str_contains($stored, "\"reason\":\"DMARC policy\"") || !is_array($rows) || count($rows) !== 7) {
        fwrite(STDERR, "DMARC failure was not quarantined before SMTP acknowledgment.\n"); exit(1);
    }
' "$TEMP_DIR/quarantine" "$RELAY_FILE"

printf 'From: Sender <sender@missing.example.test>\r\nSubject: gateway DMARC np smoke\r\n\r\nnonexistent author\r\n' |
    "$CLIENT_BIN" -addr "127.0.0.1:$SMTP_PORT" -to 'first@managed.test' -message-file -
php -r '
    $files = glob($argv[1]."/*.quarantine");
    $rows = file($argv[2], FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES);
    $matches = 0;
    foreach ($files as $file) {
        if (str_contains((string) file_get_contents($file), "Subject: gateway DMARC np smoke")) $matches++;
    }
    if (count($files) !== 2 || $matches !== 1 || !is_array($rows) || count($rows) !== 7) {
        fwrite(STDERR, "NXDOMAIN-only DMARC np policy did not quarantine the message.\n"); exit(1);
    }
' "$TEMP_DIR/quarantine" "$RELAY_FILE"

# Stop the original Planner and Spinner before starting a fresh pair. The
# return token must survive both processes, using only the persisted database
# and configured signing key.
kill "$GO_PID" "$PHP_PID"
wait "$GO_PID" 2>/dev/null || true
wait "$PHP_PID" 2>/dev/null || true
GO_PID=""
PHP_PID=""
(
    cd "$APP_PATH"
    APP_ENV=testing \
    APP_KEY="base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" \
    COMPOSER_VENDOR_DIR="$ROOT/vendor" \
    GATEWAY_INTERNAL_TOKEN="$TOKEN" \
    REGISTRY_TOKEN="$REGISTRY_TOKEN" \
    GATEWAY_PATH_RESOLVER='Webong\Gateway\Tests\Fixtures\HttpSmokePathResolver' \
    GATEWAY_HTTP_REGISTRY_SMOKE=1 \
    GATEWAY_SMOKE_SKIP_MIGRATIONS=1 \
    GATEWAY_EMAIL_SMOKE=1 \
    GATEWAY_EMAIL_POLICY='Webong\Gateway\Tests\Fixtures\EmailSmokeMessagePolicy' \
    GATEWAY_EMAIL_VERIFICATION_TTL_SECONDS=0 \
    GATEWAY_EMAIL_SRS_ENABLED=true \
    GATEWAY_EMAIL_SRS_DOMAIN=managed.test \
    GATEWAY_EMAIL_SRS_KEYS="[\"$SRS_KEY\"]" \
    DB_CONNECTION=sqlite DB_DATABASE="$DB_FILE" \
    exec php -S "127.0.0.1:$PHP2_PORT" -t public public/index.php
) >"$PHP2_LOG" 2>&1 &
PHP2_PID=$!
wait_http "http://127.0.0.1:$PHP2_PORT/http-smoke/worker" "$PHP2_LOG" "$PHP2_PID"

GATEWAY_RUNTIME=http \
GATEWAY_LARAVEL_BACKEND_URL="http://127.0.0.1:$PHP2_PORT" \
GATEWAY_INTERNAL_TOKEN="$TOKEN" \
GATEWAY_SMTP_ADDR="127.0.0.1:$SMTP2_PORT" \
GATEWAY_SMTP_HOSTNAME='smtp.managed.test' \
GATEWAY_SMTP_RELAY_ADDR="127.0.0.1:$RELAY_PORT" \
GATEWAY_SMTP_RELAY_TLS_MODE=none \
GATEWAY_DELIVERY_SPOOL_PATH="$TEMP_DIR/spool-2" \
PORT="$GO2_PORT" \
"$GO_BIN" >"$GO2_LOG" 2>&1 &
GO2_PID=$!
wait_http "http://127.0.0.1:$GO2_PORT/health" "$GO2_LOG" "$GO2_PID"

"$CLIENT_BIN" -addr "127.0.0.1:$SMTP2_PORT" -from '' -to "$srs_address" -subject 'gateway bounce smoke'
"$CLIENT_BIN" -addr "127.0.0.1:$SMTP2_PORT" -from '' -to "$uncertain_srs_address" -subject 'gateway uncertain bounce smoke'
"$CLIENT_BIN" -addr "127.0.0.1:$SMTP2_PORT" -from 'forged@example.test' -to "$srs_address" -expect-rcpt-code 550
"$CLIENT_BIN" -addr "127.0.0.1:$SMTP2_PORT" -from '' -to "${srs_address/SRS0-/SRS0-x}" -expect-rcpt-code 550
php -r '
    $rows = file($argv[1], FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES);
    $bounce = is_array($rows) && count($rows) === 9 ? json_decode($rows[7], true) : null;
    $uncertain = is_array($rows) && count($rows) === 9 ? json_decode($rows[8], true) : null;
    if (!is_array($bounce) || $bounce["from"] !== "" || $bounce["recipients"] !== ["sender@example.test"] ||
        !str_contains($bounce["data"], "Subject: gateway bounce smoke") ||
        !is_array($uncertain) || $uncertain["from"] !== "" || $uncertain["recipients"] !== ["sender@example.test"] ||
        !str_contains($uncertain["data"], "Subject: gateway uncertain bounce smoke")) {
        fwrite(STDERR, "Valid null-sender bounce was not reverse-routed after SMTP uncertainty.\n"); exit(1);
    }
' "$RELAY_FILE"

echo 'Email routing, policy reject/defer, DMARC quarantine/np, lost-ack/process-turnover SRS bounce reversal, and forwarding-loop smoke passed.'
