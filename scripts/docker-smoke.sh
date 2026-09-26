#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${GATEWAY_DOCKER_IMAGE:-gateway:smoke}"
NAME="gateway-container-smoke-$$"

cleanup() {
    docker rm -f "$NAME" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

docker build -t "$IMAGE" "$ROOT"
docker run -d --rm --name "$NAME" -e GATEWAY_RUNTIME=diagnostic -p 127.0.0.1::5001 "$IMAGE" >/dev/null

port="$(docker inspect -f '{{(index (index .NetworkSettings.Ports "5001/tcp") 0).HostPort}}' "$NAME")"
for _ in $(seq 1 30); do
    if curl -fsS --max-time 1 "http://127.0.0.1:${port}/health" >/dev/null; then
        break
    fi
    sleep 0.25
done

curl -fsS --max-time 2 "http://127.0.0.1:${port}/health" >/dev/null
user="$(docker inspect -f '{{.Config.User}}' "$NAME")"
if [[ "$user" != "gateway" ]]; then
    echo "Gateway container is unexpectedly running as ${user:-root}." >&2
    exit 1
fi

echo "Gateway router container smoke passed."
