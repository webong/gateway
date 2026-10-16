#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PORT="${GATEWAY_SCHEDULE_RR_PORT:-18391}"
ADMIN_PORT="${GATEWAY_SCHEDULE_ADMIN_PORT:-18392}"
CALLBACK_PORT="${GATEWAY_SCHEDULE_CALLBACK_PORT:-18393}"
TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/gateway-schedule-rr.XXXXXX")"
ROUTER_BIN="$TEMP_DIR/gateway-router-roadrunner"
ROUTER_LOG="$TEMP_DIR/router.log"
SCHEDULE_RESPONSE="$TEMP_DIR/schedule.json"
RUNS_RESPONSE="$TEMP_DIR/runs.json"
ROUTER_PID=""
CALLBACK_PID=""

cleanup() {
    local status=$?
    if [[ -n "$CALLBACK_PID" ]] && kill -0 "$CALLBACK_PID" 2>/dev/null; then
        kill "$CALLBACK_PID" 2>/dev/null || true
        wait "$CALLBACK_PID" 2>/dev/null || true
    fi
    if [[ -n "$ROUTER_PID" ]] && kill -0 "$ROUTER_PID" 2>/dev/null; then
        kill "$ROUTER_PID" 2>/dev/null || true
        wait "$ROUTER_PID" 2>/dev/null || true
    fi
    if [[ "$status" -ne 0 ]]; then
        echo "RoadRunner schedule smoke diagnostics preserved at $TEMP_DIR" >&2
        return
    fi
    rm -r -- "$TEMP_DIR"
}

trap cleanup EXIT INT TERM

if [[ ! -x "$ROOT/vendor/bin/roadrunner-worker" || ! -f "$ROOT/vendor/autoload.php" ]]; then
    echo 'Composer dependencies are required for the RoadRunner schedule smoke.' >&2
    exit 1
fi

# The smoke build permits loopback callbacks. Serve a real HTTP 200 response
# so the scheduled assertion can observe a terminal 204-versus-200 mismatch.
php -S "127.0.0.1:$CALLBACK_PORT" -t "$ROOT" >"$TEMP_DIR/callback.log" 2>&1 &
CALLBACK_PID=$!
for _ in $(seq 1 40); do
    if curl -fsS --max-time 1 "http://127.0.0.1:$CALLBACK_PORT/README.md" >/dev/null 2>&1; then
        break
    fi
    if ! kill -0 "$CALLBACK_PID" 2>/dev/null; then
        tail -40 "$TEMP_DIR/callback.log" >&2
        exit 1
    fi
    sleep 0.1
done
if ! curl -fsS --max-time 2 "http://127.0.0.1:$CALLBACK_PORT/README.md" >/dev/null ||
    ! curl -fsS --max-time 2 -X POST -d '{}' "http://127.0.0.1:$CALLBACK_PORT/README.md" >/dev/null; then
    tail -40 "$TEMP_DIR/callback.log" >&2
    exit 1
fi
README_DIGEST="$(php -r 'echo hash_file("sha256", $argv[1]);' "$ROOT/README.md")"

go -C "$ROOT/ext/roadrunner/cmd/router" build -mod=mod -tags=gateway_lua,gateway_smoke -o "$ROUTER_BIN" .

GATEWAY_ROUTER_DATA_DIR="$TEMP_DIR/data" \
GATEWAY_ROUTER_ROADRUNNER_BIN="$ROUTER_BIN" \
GATEWAY_ROUTER_LISTEN="127.0.0.1:$PORT" \
GATEWAY_ROUTER_PHP_WORKERS=1 \
GATEWAY_SCHEDULES_ENABLED=true \
GATEWAY_SCHEDULE_POLL_INTERVAL=200ms \
GATEWAY_DELIVERY_ADMIN_ADDR="127.0.0.1:$ADMIN_PORT" \
GATEWAY_DELIVERY_ADMIN_URL="http://127.0.0.1:$ADMIN_PORT" \
GATEWAY_SCHEDULE_NOTIFICATION_ALLOWED_HOSTS=127.0.0.1 \
LOG_LEVEL=error \
sh "$ROOT/scripts/router-start" roadrunner >"$ROUTER_LOG" 2>&1 &
ROUTER_PID=$!

for _ in $(seq 1 120); do
    if curl -fsS --max-time 1 "http://127.0.0.1:$PORT/health" >/dev/null 2>&1; then
        break
    fi
    if ! kill -0 "$ROUTER_PID" 2>/dev/null; then
        tail -80 "$ROUTER_LOG" >&2
        exit 1
    fi
    sleep 0.25
done
if ! curl -fsS --max-time 2 "http://127.0.0.1:$PORT/health" >/dev/null; then
    tail -80 "$ROUTER_LOG" >&2
    exit 1
fi

# This file belongs to the throwaway data directory created by router-start.
# Do not print the generated credentials into test output.
set -a
source "$TEMP_DIR/data/gateway.env"
set +a

curl -fsS --max-time 10 -X POST "http://127.0.0.1:$PORT/schedules/notification-destination" \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    -H 'Content-Type: application/json' \
    --data-binary "{\"url\":\"http://127.0.0.1:$CALLBACK_PORT/README.md\"}" >/dev/null

curl -fsS --max-time 10 -X POST "http://127.0.0.1:$PORT/schedules" \
    -H "Authorization: Bearer $REGISTRY_TOKEN" \
    -H 'Content-Type: application/json' \
    --data-binary @- >"$SCHEDULE_RESPONSE" <<'JSON'
{"name":"RoadRunner smoke","cron":"* * * * *","timezone":"UTC","language":"lua","source":"gateway.schedule({key = \"child\", name = \"Child\", cron = \"0 0 * * *\", language = \"lua\", source = \"gateway.respond('done', 200)\"})","payload":{"source":"smoke"}}
JSON

SCHEDULE_ID="$(php -r '$value=json_decode(file_get_contents($argv[1]), true, 512, JSON_THROW_ON_ERROR); $id=$value["id"] ?? ""; if (!is_string($id) || $id === "") exit(1); echo $id;' "$SCHEDULE_RESPONSE")"

php -r '
    $db = new PDO("sqlite:".$argv[1]);
    $stmt = $db->prepare("UPDATE schedules SET next_run_at = datetime(\x27now\x27, \x27-1 minute\x27) WHERE id = ?");
    $stmt->execute([$argv[2]]);
    if ($stmt->rowCount() !== 1) exit(1);
' "$TEMP_DIR/data/gateway.sqlite" "$SCHEDULE_ID"

for _ in $(seq 1 75); do
    if ! kill -0 "$ROUTER_PID" 2>/dev/null; then
        tail -80 "$ROUTER_LOG" >&2
        exit 1
    fi
    curl -fsS --max-time 3 \
        -H "Authorization: Bearer $REGISTRY_TOKEN" \
        "http://127.0.0.1:$PORT/schedules/$SCHEDULE_ID/runs" >"$RUNS_RESPONSE"
    if php -r '
        $value=json_decode(file_get_contents($argv[1]), true, 512, JSON_THROW_ON_ERROR);
        $run=$value["data"][0] ?? null;
        exit(is_array($run) && ($run["status"] ?? null) === "succeeded" && ($run["deliveries_queued"] ?? null) === 0 ? 0 : 1);
    ' "$RUNS_RESPONSE"; then
        curl -fsS --max-time 3 \
            -H "Authorization: Bearer $REGISTRY_TOKEN" \
            "http://127.0.0.1:$PORT/schedules" >"$TEMP_DIR/schedules.json"
        php -r '
            $value=json_decode(file_get_contents($argv[1]), true, 512, JSON_THROW_ON_ERROR);
            foreach ($value["data"] ?? [] as $schedule) {
                if (($schedule["origin"] ?? null) === "schedule:".$argv[2] && ($schedule["key"] ?? null) === "child") exit(0);
            }
            exit(1);
        ' "$TEMP_DIR/schedules.json" "$SCHEDULE_ID"

        curl -fsS --max-time 3 -X POST \
            -H "Authorization: Bearer $REGISTRY_TOKEN" \
            -H 'Idempotency-Key: smoke-manual' \
            -H 'Content-Type: application/json' \
            --data '{}' "http://127.0.0.1:$PORT/schedules/$SCHEDULE_ID/runs" >"$TEMP_DIR/manual.json"
        MANUAL_ID="$(php -r '$value=json_decode(file_get_contents($argv[1]), true, 512, JSON_THROW_ON_ERROR); if (($value["status"] ?? null) !== "pending") exit(1); echo $value["id"] ?? "";' "$TEMP_DIR/manual.json")"
        curl -fsS --max-time 3 -X POST \
            -H "Authorization: Bearer $REGISTRY_TOKEN" \
            -H 'Idempotency-Key: smoke-manual' \
            -H 'Content-Type: application/json' \
            --data '{}' "http://127.0.0.1:$PORT/schedules/$SCHEDULE_ID/runs" >"$TEMP_DIR/manual-repeat.json"
        php -r '$value=json_decode(file_get_contents($argv[1]), true, 512, JSON_THROW_ON_ERROR); exit(($value["id"] ?? null) === $argv[2] ? 0 : 1);' \
            "$TEMP_DIR/manual-repeat.json" "$MANUAL_ID"

        for _ in $(seq 1 75); do
            curl -fsS --max-time 3 \
                -H "Authorization: Bearer $REGISTRY_TOKEN" \
                "http://127.0.0.1:$PORT/schedules/$SCHEDULE_ID/runs" >"$RUNS_RESPONSE"
            if php -r '
                $value=json_decode(file_get_contents($argv[1]), true, 512, JSON_THROW_ON_ERROR);
                foreach ($value["data"] ?? [] as $run) {
                    if (($run["id"] ?? null) === $argv[2] && ($run["kind"] ?? null) === "manual" && ($run["status"] ?? null) === "succeeded") exit(0);
                }
                exit(1);
            ' "$RUNS_RESPONSE" "$MANUAL_ID"; then
                curl -fsS --max-time 3 -X POST "http://127.0.0.1:$PORT/schedules" \
                    -H "Authorization: Bearer $REGISTRY_TOKEN" \
                    -H 'Content-Type: application/json' \
                    --data "{\"name\":\"Delivery probe\",\"cron\":\"0 0 * * *\",\"timezone\":\"UTC\",\"language\":\"lua\",\"source\":\"gateway.forward({url = \\\"http://127.0.0.1:$CALLBACK_PORT/README.md\\\", method = \\\"GET\\\", expected_status = 200, expected_body_sha256 = \\\"$README_DIGEST\\\"}); gateway.forward({url = \\\"http://127.0.0.1:$CALLBACK_PORT/README.md\\\", method = \\\"GET\\\", expected_status = 204}); gateway.forward({url = \\\"http://127.0.0.1:$CALLBACK_PORT/README.md\\\", method = \\\"GET\\\", expected_status = 200, expected_body_sha256 = \\\"0000000000000000000000000000000000000000000000000000000000000000\\\"})\"}" \
                    >"$TEMP_DIR/delivery-schedule.json"
                DELIVERY_SCHEDULE_ID="$(php -r '$v=json_decode(file_get_contents($argv[1]), true, 512, JSON_THROW_ON_ERROR); echo $v["id"] ?? "";' "$TEMP_DIR/delivery-schedule.json")"
                curl -fsS --max-time 3 -X POST \
                    -H "Authorization: Bearer $REGISTRY_TOKEN" \
                    -H 'Content-Type: application/json' \
                    --data '{}' "http://127.0.0.1:$PORT/schedules/$DELIVERY_SCHEDULE_ID/runs" >"$TEMP_DIR/delivery-run.json"
                DELIVERY_RUN_ID="$(php -r '$v=json_decode(file_get_contents($argv[1]), true, 512, JSON_THROW_ON_ERROR); echo $v["id"] ?? "";' "$TEMP_DIR/delivery-run.json")"
                for _ in $(seq 1 75); do
                    curl -fsS --max-time 3 -H "Authorization: Bearer $REGISTRY_TOKEN" \
                        "http://127.0.0.1:$PORT/schedules/$DELIVERY_SCHEDULE_ID/runs" >"$TEMP_DIR/delivery-runs.json"
                    curl -fsS --max-time 3 -H "Authorization: Bearer $REGISTRY_TOKEN" \
                        "http://127.0.0.1:$PORT/deliveries?event_id=$DELIVERY_RUN_ID" >"$TEMP_DIR/delivery-status.json"
                    curl -fsS --max-time 3 -H "Authorization: Bearer $REGISTRY_TOKEN" \
                        "http://127.0.0.1:$PORT/schedules/$DELIVERY_SCHEDULE_ID/runs/$DELIVERY_RUN_ID/outcome" >"$TEMP_DIR/delivery-outcome.json"
                    if php -r '
                        $runs=json_decode(file_get_contents($argv[1]), true, 512, JSON_THROW_ON_ERROR)["data"] ?? [];
                        $items=json_decode(file_get_contents($argv[2]), true, 512, JSON_THROW_ON_ERROR)["data"] ?? [];
                        $outcome=json_decode(file_get_contents($argv[4]), true, 512, JSON_THROW_ON_ERROR);
                        $run=$runs[0] ?? [];
                        $matched=false;
                        $mismatched=false;
                        $bodyMismatched=false;
                        foreach ($items as $item) {
                            if (($item["event_id"] ?? null) !== $argv[3] || isset($item["target"]) || isset($item["payload"])) exit(1);
                            $matched = $matched || (($item["state"] ?? null) === "delivered" && ($item["expected_status"] ?? null) === 200 && ($item["response_status"] ?? null) === 200 && ($item["body_assertion"] ?? null) === "matched");
                            $mismatched = $mismatched || (($item["state"] ?? null) === "failed" && ($item["expected_status"] ?? null) === 204 && ($item["response_status"] ?? null) === 200 && !isset($item["body_assertion"]));
                            $bodyMismatched = $bodyMismatched || (($item["state"] ?? null) === "failed" && ($item["expected_status"] ?? null) === 200 && ($item["response_status"] ?? null) === 200 && ($item["body_assertion"] ?? null) === "mismatched");
                        }
                        exit(($run["id"] ?? null) === $argv[3] && ($run["status"] ?? null) === "succeeded" &&
                            ($run["deliveries_queued"] ?? null) === 3 &&
                            ($run["deliveries_url"] ?? null) === "/deliveries?event_id=".$argv[3] &&
                            ($run["outcome_url"] ?? null) === "/schedules/".$argv[5]."/runs/".$argv[3]."/outcome" &&
                            count($items) === 3 && $matched && $mismatched && $bodyMismatched &&
                            ($outcome["execution_status"] ?? null) === "succeeded" &&
                            ($outcome["deliveries_observed"] ?? null) === 3 &&
                            ($outcome["delivery_status"] ?? null) === "failed" ? 0 : 1);
                    ' "$TEMP_DIR/delivery-runs.json" "$TEMP_DIR/delivery-status.json" "$DELIVERY_RUN_ID" "$TEMP_DIR/delivery-outcome.json" "$DELIVERY_SCHEDULE_ID"; then
                        for _ in $(seq 1 75); do
                            if php -r '
                                $db = new PDO("sqlite:".$argv[1]);
                                $stmt = $db->prepare("SELECT state FROM schedule_notifications WHERE run_id = ?");
                                $stmt->execute([$argv[2]]);
                                exit($stmt->fetchColumn() === "sent" ? 0 : 1);
                            ' "$TEMP_DIR/data/gateway.sqlite" "$DELIVERY_RUN_ID"; then
                                echo 'Bundled RoadRunner cron, manual run, assertions, outcome, and automatic alert smoke passed.'
                                exit 0
                            fi
                            sleep 0.2
                        done
                        echo 'RoadRunner did not send the failed-run notification.' >&2
                        tail -80 "$ROUTER_LOG" >&2
                        exit 1
                    fi
                    sleep 0.2
                done
                echo 'RoadRunner run did not correlate with its queued delivery.' >&2
                tail -80 "$ROUTER_LOG" >&2
                exit 1
            fi
            sleep 0.2
        done
        echo 'RoadRunner did not complete the manual Lua run.' >&2
        tail -80 "$ROUTER_LOG" >&2
        exit 1
    fi
    sleep 0.2
done

echo 'RoadRunner did not complete the due Lua schedule.' >&2
tail -80 "$ROUTER_LOG" >&2
exit 1
