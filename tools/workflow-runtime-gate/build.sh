#!/usr/bin/env bash
set -euo pipefail
source=${1:?Usage: build.sh CLEAN_WORKFLOW_SOURCE amd64|arm64}
arch=${2:?Usage: build.sh CLEAN_WORKFLOW_SOURCE amd64|arm64}
case "$arch" in amd64|arm64) ;; *) echo 'Only Linux amd64 and arm64 are in this gate.' >&2; exit 2 ;; esac
expected=00e0bbcdd61c2c6ed96af55865d0ef2e77557adb
if [[ "$(git -C "$source" rev-parse HEAD)" != "$expected" || -n "$(git -C "$source" status --porcelain)" ]]; then
    echo 'Use the clean merged WorkFlow prerequisite worktree; dirty or different sources are rejected.' >&2
    exit 1
fi
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
mkdir -p tools/workflow-runtime-gate/build
GOOS=linux GOARCH="$arch" CGO_ENABLED=0 GOWORK=off \
    go -C tools/workflow-runtime-gate build -mod=readonly -trimpath -o build/spinner .
source_image="gateway-workflow-runtime-gate:source-$arch"
docker build --platform "linux/$arch" --label "gateway.workflow.source=$expected" \
    -t "$source_image" -f "$source/app/Dockerfile" "$source"
if [[ "$(docker image inspect "$source_image" --format '{{.Architecture}}')" != "$arch" ]]; then
    echo 'Source image architecture does not match the artifact.' >&2; exit 1
fi
docker build --platform "linux/$arch" --build-context "workflow=$source" \
    --build-arg "WORKFLOW_IMAGE=$source_image" --build-arg "TARGETARCH=$arch" \
    -f tools/workflow-runtime-gate/Dockerfile -t "gateway-workflow-runtime-gate:$arch" .
