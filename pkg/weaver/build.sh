#!/usr/bin/env bash
set -euo pipefail
arch=${1:?Pass amd64 or arm64}
case "$arch" in amd64|arm64) ;; *) exit 2 ;; esac
root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
pinned_runtime=ghcr.io/webong/workflow@sha256:dc876bfa06d295e4049a5658ef813d56a536db22c069da674633151664813d9e
source_image=${WEAVER_SOURCE_IMAGE:-$pinned_runtime}
if [[ ${WEAVER_VERSION:-0.1.0-dev} != *dev* && "$source_image" != "$pinned_runtime" ]]; then
    echo "Release candidates require the reviewed immutable runtime image." >&2; exit 2
fi
if [[ "$source_image" == "$pinned_runtime" && $(docker image inspect "$source_image" --format '{{.Architecture}}' 2>/dev/null || true) != "$arch" ]]; then
    docker pull --platform "linux/$arch" "$source_image"
fi
[[ $(docker image inspect "$source_image" --format '{{.Architecture}}') == "$arch" ]]
revision=$(git rev-parse HEAD)
if [[ -n $(git status --porcelain) ]]; then revision="$revision-dirty"; fi
docker build --platform "linux/$arch" --build-arg "WORKFLOW_IMAGE=$source_image" \
    --build-arg "WEAVER_REVISION=$revision" --build-arg "WEAVER_VERSION=${WEAVER_VERSION:-0.1.0-dev}" --build-arg "TARGETARCH=$arch" \
    -t "gateway-weaver:$arch" -f pkg/weaver/Dockerfile .
