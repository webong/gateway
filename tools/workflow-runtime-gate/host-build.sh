#!/usr/bin/env bash
set -euo pipefail
host=${1:?Pass spinner, roadrunner or frankenphp}
case "$host" in
 spinner) docker build -f infra/docker/Gateway.spinner.Dockerfile -t gateway-workflow-host:spinner . ;;
 roadrunner|frankenphp) docker build --build-arg "GATEWAY_ROUTER_RUNTIME=$host" -f infra/docker/Gateway.router.Dockerfile -t "gateway-workflow-base:$host" .
 docker build --build-arg "GATEWAY_PLANNER_IMAGE=gateway-workflow-base:$host" --target installed -f pkg/weaver/planner-test.Dockerfile -t "gateway-workflow-host:$host" . ;;
 *) exit 2 ;;
esac
