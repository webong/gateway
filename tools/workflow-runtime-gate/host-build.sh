#!/usr/bin/env bash
set -euo pipefail
host=${1:?Pass spinner, roadrunner or frankenphp}
case "$host" in
 spinner) docker build -f infra/docker/Gateway.spinner.Dockerfile -t gateway-workflow-host:spinner . ;;
 roadrunner|frankenphp) docker build --build-arg "GATEWAY_ROUTER_RUNTIME=$host" -f infra/docker/Gateway.router.Dockerfile -t "gateway-workflow-host:$host" . ;;
 *) exit 2 ;;
esac
