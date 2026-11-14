#!/usr/bin/env bash
set -euo pipefail
arch=${1:?Pass amd64 or arm64}
case "$arch" in amd64|arm64) ;; *) exit 2 ;; esac
root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
source_image=${WEAVER_SOURCE_IMAGE:-gateway-workflow-runtime-gate:source-$arch}
[[ $(docker image inspect "$source_image" --format '{{.Architecture}}') == "$arch" ]]
mkdir -p pkg/weaver/build
GOOS=linux GOARCH="$arch" CGO_ENABLED=0 GOWORK=off \
    go -C pkg/weaver build -mod=readonly -trimpath -o build/spinner ./cmd/spinner
revision=$(git rev-parse HEAD)
if [[ -n $(git status --porcelain) ]]; then revision="$revision-dirty"; fi
docker build --platform "linux/$arch" --build-arg "WORKFLOW_IMAGE=$source_image" \
    --build-arg "WEAVER_REVISION=$revision" --build-arg "TARGETARCH=$arch" \
    -t "gateway-weaver:$arch" -f pkg/weaver/Dockerfile .
