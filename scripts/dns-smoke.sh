#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_PATH="${GATEWAY_DNS_APP_PATH:-$ROOT/tests/Fixtures/octane-app}"
PHP_PORT="${GATEWAY_DNS_PHP_PORT:-18291}"
GO_PORT="${GATEWAY_DNS_GO_PORT:-18292}"
DNS_PORT="${GATEWAY_DNS_SMOKE_PORT:-12553}"
GO2_PORT="${GATEWAY_DNS_GO2_PORT:-18293}"
DNS2_PORT="${GATEWAY_DNS_SMOKE_PORT_2:-12554}"
DELEGATION_PORT="${GATEWAY_DNS_DELEGATION_PORT:-12555}"
TOKEN="${GATEWAY_INTERNAL_TOKEN:-dns-smoke-token}"
REGISTRY_TOKEN="${REGISTRY_TOKEN:-dns-registry-smoke-token}"
TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/gateway-dns.XXXXXX")"
DB_FILE="$TEMP_DIR/gateway.sqlite"
PHP_LOG="$TEMP_DIR/php.log"
GO_LOG="$TEMP_DIR/spinner.log"
GO2_LOG="$TEMP_DIR/spinner-2.log"
GO_BIN="$TEMP_DIR/gateway"
CHECK_BIN="$TEMP_DIR/dns-delegation-check"
DELEGATION_BIN="$TEMP_DIR/dns-delegation-fixture"
DELEGATION_LOG="$TEMP_DIR/delegation.log"
PHP_PID=""
GO_PID=""
GO2_PID=""
DELEGATION_PID=""

cleanup() {
    local status=$?
    for pid in "$DELEGATION_PID" "$GO2_PID" "$GO_PID" "$PHP_PID"; do
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
        fi
    done
    if [[ "$status" -ne 0 ]]; then
        echo "DNS smoke diagnostics preserved at $TEMP_DIR" >&2
        return
    fi
    case "$TEMP_DIR" in
        "${TMPDIR:-/tmp}"/gateway-dns.*) rm -r -- "$TEMP_DIR" ;;
    esac
}
trap cleanup EXIT INT TERM

if [[ ! -f "$APP_PATH/bootstrap/app.php" || ! -f "$ROOT/vendor/autoload.php" ]]; then
    echo 'Laravel DNS fixture and Composer dependencies are required.' >&2
    exit 1
fi
if ! command -v dig >/dev/null 2>&1; then
    echo 'dig is required for the DNS integration smoke.' >&2
    exit 1
fi

touch "$DB_FILE"
GO_BUILD_FLAGS="${GOFLAGS:--mod=mod}"
go build "$GO_BUILD_FLAGS" -o "$GO_BIN" "$ROOT/src/spinner/cmd/proxy"
go build "$GO_BUILD_FLAGS" -o "$CHECK_BIN" "$ROOT/src/spinner/cmd/dns-delegation-check"
go build "$GO_BUILD_FLAGS" -o "$DELEGATION_BIN" "$ROOT/tests/Fixtures/dns-delegation-fixture"

(
    cd "$APP_PATH"
    APP_ENV=testing APP_KEY="base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" \
    COMPOSER_VENDOR_DIR="$ROOT/vendor" DB_CONNECTION=sqlite DB_DATABASE="$DB_FILE" \
    GATEWAY_HTTP_REGISTRY_SMOKE=1 GATEWAY_DNS_SMOKE=1 \
    GATEWAY_PATH_RESOLVER='Webong\Gateway\Tests\Fixtures\HttpSmokePathResolver' \
    GATEWAY_INTERNAL_TOKEN="$TOKEN" REGISTRY_TOKEN="$REGISTRY_TOKEN" \
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
GATEWAY_INTERNAL_TOKEN="$TOKEN" GATEWAY_DNS_ADDR="127.0.0.1:$DNS_PORT" \
GATEWAY_DNS_ZONE_REFRESH_INTERVAL=100ms PORT="$GO_PORT" LOG_LEVEL=error \
"$GO_BIN" >"$GO_LOG" 2>&1 &
GO_PID=$!
wait_http "http://127.0.0.1:$GO_PORT/health" "$GO_LOG" "$GO_PID"

GATEWAY_RUNTIME=http GATEWAY_PLANNER_URL="http://127.0.0.1:$PHP_PORT" \
GATEWAY_INTERNAL_TOKEN="$TOKEN" GATEWAY_DNS_ADDR="127.0.0.1:$DNS2_PORT" \
GATEWAY_DNS_ZONE_REFRESH_INTERVAL=100ms PORT="$GO2_PORT" LOG_LEVEL=error \
"$GO_BIN" >"$GO2_LOG" 2>&1 &
GO2_PID=$!
wait_http "http://127.0.0.1:$GO2_PORT/health" "$GO2_LOG" "$GO2_PID"

"$DELEGATION_BIN" -addr "127.0.0.1:$DELEGATION_PORT" >"$DELEGATION_LOG" 2>&1 &
DELEGATION_PID=$!
for _ in $(seq 1 30); do
    if [[ "$(dig @127.0.0.1 -p "$DELEGATION_PORT" +norecurse +time=1 +tries=1 +short example.test SOA 2>/dev/null)" == *'ns1.example.net.'* ]]; then break; fi
    if ! kill -0 "$DELEGATION_PID" 2>/dev/null; then
        tail -80 "$DELEGATION_LOG" >&2
        exit 1
    fi
    sleep 0.1
done

planner="http://127.0.0.1:$PHP_PORT"
auth="Authorization: Bearer $REGISTRY_TOKEN"
curl -fsS "$planner/dns/zones" -H "$auth" | php -r '
    $zones = json_decode(stream_get_contents(STDIN), true)["data"] ?? [];
    if (count($zones) !== 0) exit(1);
'
curl -fsS -X POST "$planner/dns/zones" -H "$auth" -H 'Content-Type: application/json' \
    -d '{"name":"first.example.test","nameservers":["ns1.example.net","ns2.example.net"],"ttl":60}' >/dev/null
curl -fsS -X POST "$planner/dns/zones" -H "$auth" -H 'Content-Type: application/json' \
    -d '{"name":"second.example.test","nameservers":["ns1.example.net","ns2.example.net"],"ttl":60}' >/dev/null
for _ in $(seq 1 50); do
    primary_zone="$(dig @127.0.0.1 -p "$DNS_PORT" +time=1 +tries=1 +short first.example.test NS 2>/dev/null | LC_ALL=C sort)"
    peer_zone="$(dig @127.0.0.1 -p "$DNS2_PORT" +time=1 +tries=1 +short second.example.test NS 2>/dev/null | LC_ALL=C sort)"
    if [[ "$primary_zone" == $'ns1.example.net.\nns2.example.net.' && "$peer_zone" == $'ns1.example.net.\nns2.example.net.' ]]; then break; fi
    sleep 0.1
done
[[ "$primary_zone" == $'ns1.example.net.\nns2.example.net.' && "$peer_zone" == $'ns1.example.net.\nns2.example.net.' ]]
curl -fsS -X PUT "$planner/dns/zones/first.example.test/records" -H "$auth" -H 'Content-Type: application/json' \
    -d '{"records":[{"name":"first.example.test","type":"MX","value":"route1.mx.cloudflare.net","preference":10}]}' >/dev/null
for _ in $(seq 1 50); do
    primary_mx="$(dig @127.0.0.1 -p "$DNS_PORT" +time=1 +tries=1 +short first.example.test MX 2>/dev/null)"
    peer_mx="$(dig +tcp @127.0.0.1 -p "$DNS2_PORT" +time=1 +tries=1 +short first.example.test MX 2>/dev/null)"
    if [[ "$primary_mx" == '10 route1.mx.cloudflare.net.' && "$peer_mx" == "$primary_mx" ]]; then break; fi
    sleep 0.1
done
[[ "$primary_mx" == '10 route1.mx.cloudflare.net.' && "$peer_mx" == "$primary_mx" ]]
first="$(curl -fsS -X POST "$planner/dns/hooks" -H "$auth" -H 'Content-Type: application/json' -d '{"external_id":"smoke","zone":"first.example.test"}')"
second="$(curl -fsS -X POST "$planner/dns/hooks" -H "$auth" -H 'Content-Type: application/json' -d '{"external_id":"smoke","zone":"second.example.test"}')"
first_id="$(printf '%s' "$first" | php -r '$v=json_decode(stream_get_contents(STDIN),true); echo $v["id"] ?? "";')"
first_token="$(printf '%s' "$first" | php -r '$v=json_decode(stream_get_contents(STDIN),true); echo $v["token"] ?? "";')"
second_id="$(printf '%s' "$second" | php -r '$v=json_decode(stream_get_contents(STDIN),true); echo $v["id"] ?? "";')"
second_token="$(printf '%s' "$second" | php -r '$v=json_decode(stream_get_contents(STDIN),true); echo $v["token"] ?? "";')"
if [[ -z "$first_token" || -z "$second_token" || "$first_token" == "$second_token" ]]; then
    echo 'DNS hook creation did not return distinct tokens.' >&2
    exit 1
fi

first_answer="$(dig @127.0.0.1 -p "$DNS_PORT" +time=1 +tries=2 +noall +comments +authority "hello.$first_token.first.example.test" TXT)"
second_answer="$(dig +tcp @127.0.0.1 -p "$DNS_PORT" +time=1 +tries=2 +noall +comments +authority "hello.$second_token.second.example.test" TXT)"
[[ "$first_answer" == *'status: NOERROR'* && "$first_answer" == *'first.example.test.'* ]]
[[ "$second_answer" == *'status: NOERROR'* && "$second_answer" == *'second.example.test.'* ]]
wrong_answer="$(dig @127.0.0.1 -p "$DNS_PORT" +time=1 +tries=2 +noall +comments "hello.$first_token.second.example.test" TXT)"
[[ "$wrong_answer" == *'status: NXDOMAIN'* ]]

curl -fsS "$planner/dns/hooks/$first_id/events" -H "$auth" | php -r '
    $events = json_decode(stream_get_contents(STDIN), true)["data"] ?? [];
    if (count($events) !== 1 || !str_ends_with($events[0]["query"]["name"] ?? "", ".first.example.test.")) exit(1);
'
curl -fsS "$planner/dns/hooks/$second_id/events" -H "$auth" | php -r '
    $events = json_decode(stream_get_contents(STDIN), true)["data"] ?? [];
    if (count($events) !== 1 || !str_ends_with($events[0]["query"]["name"] ?? "", ".second.example.test.")) exit(1);
'

peer_first="$(dig +tcp @127.0.0.1 -p "$DNS2_PORT" +time=1 +tries=2 +noall +comments +authority "hello.$first_token.first.example.test" TXT)"
peer_second="$(dig @127.0.0.1 -p "$DNS2_PORT" +time=1 +tries=2 +noall +comments +authority "hello.$second_token.second.example.test" TXT)"
[[ "$peer_first" == *'status: NOERROR'* && "$peer_first" == *'first.example.test.'* ]]
[[ "$peer_second" == *'status: NOERROR'* && "$peer_second" == *'second.example.test.'* ]]
expected_nameservers="$(printf 'ns1.example.net.\nns2.example.net.\n' | LC_ALL=C sort)"
primary_nameservers="$(dig @127.0.0.1 -p "$DNS_PORT" +time=1 +tries=2 +short first.example.test NS | LC_ALL=C sort)"
peer_nameservers="$(dig +tcp @127.0.0.1 -p "$DNS2_PORT" +time=1 +tries=2 +short first.example.test NS | LC_ALL=C sort)"
[[ "$primary_nameservers" == "$expected_nameservers" && "$peer_nameservers" == "$expected_nameservers" ]]

"$CHECK_BIN" \
    -zone first.example.test -parent "127.0.0.1:$DELEGATION_PORT" \
    -parent-zone example.test -resolver "127.0.0.1:$DELEGATION_PORT" \
    -nodes "127.0.0.1:$DNS_PORT,127.0.0.1:$DNS2_PORT" \
    -nameservers ns1.example.net,ns2.example.net \
    -probe "hello.$first_token.first.example.test" \
    -negative-probe hello.unregistered.first.example.test \
    -probe-type TXT -expect-mx 10:route1.mx.cloudflare.net -allow-private -timeout 20s

kill "$GO_PID"
wait "$GO_PID" 2>/dev/null || true
GO_PID=""
failover_first="$(dig @127.0.0.1 -p "$DNS2_PORT" +time=1 +tries=2 +noall +comments +authority "hello.$first_token.first.example.test" TXT)"
failover_second="$(dig +tcp @127.0.0.1 -p "$DNS2_PORT" +time=1 +tries=2 +noall +comments +authority "hello.$second_token.second.example.test" TXT)"
[[ "$failover_first" == *'status: NOERROR'* && "$failover_first" == *'first.example.test.'* ]]
[[ "$failover_second" == *'status: NOERROR'* && "$failover_second" == *'second.example.test.'* ]]
survivor_nameservers="$(dig @127.0.0.1 -p "$DNS2_PORT" +time=1 +tries=2 +short first.example.test NS | LC_ALL=C sort)"
[[ "$survivor_nameservers" == "$expected_nameservers" ]]
survivor_mx="$(dig @127.0.0.1 -p "$DNS2_PORT" +time=1 +tries=2 +short first.example.test MX)"
[[ "$survivor_mx" == '10 route1.mx.cloudflare.net.' ]]

# A restarted node is a cold process: it must reload the Planner-owned zone
# snapshot before serving, rather than depending on its previous in-memory copy.
GATEWAY_RUNTIME=http GATEWAY_PLANNER_URL="http://127.0.0.1:$PHP_PORT" \
GATEWAY_INTERNAL_TOKEN="$TOKEN" GATEWAY_DNS_ADDR="127.0.0.1:$DNS_PORT" \
GATEWAY_DNS_ZONE_REFRESH_INTERVAL=100ms PORT="$GO_PORT" LOG_LEVEL=error \
"$GO_BIN" >"$GO_LOG" 2>&1 &
GO_PID=$!
wait_http "http://127.0.0.1:$GO_PORT/health" "$GO_LOG" "$GO_PID"
reloaded_nameservers=""
for _ in $(seq 1 50); do
    reloaded_nameservers="$(dig @127.0.0.1 -p "$DNS_PORT" +time=1 +tries=1 +short first.example.test NS 2>/dev/null | LC_ALL=C sort)"
    if [[ "$reloaded_nameservers" == "$expected_nameservers" ]]; then break; fi
    sleep 0.1
done
[[ "$reloaded_nameservers" == "$expected_nameservers" ]]
reloaded_mx="$(dig +tcp @127.0.0.1 -p "$DNS_PORT" +time=1 +tries=2 +short first.example.test MX)"
[[ "$reloaded_mx" == '10 route1.mx.cloudflare.net.' ]]

curl -fsS "$planner/dns/hooks/$first_id/events" -H "$auth" | php -r '
    $events = json_decode(stream_get_contents(STDIN), true)["data"] ?? [];
    if (count($events) !== 7) exit(1);
'
curl -fsS "$planner/dns/hooks/$second_id/events" -H "$auth" | php -r '
    $events = json_decode(stream_get_contents(STDIN), true)["data"] ?? [];
    if (count($events) !== 3) exit(1);
'

echo 'DNS API-provisioned zones and MX records, two-Spinner live reload and UDP/TCP failover integration smoke passed.'
