#!/usr/bin/env bash

# Installs the Shell automation engine into a disposable Planner and Spinner the
# way a Gateway deployment does: build the `webong/shell` package, archive it,
# and activate it through Planner's `/extensions` resource. No Gateway host
# binary compiles a concrete automation engine, so a Spinner only runs Shell
# hooks after this activation has been reconciled.
#
# The one step a loopback smoke cannot perform is the HTTPS download: Planner
# and Spinner both require an `https` artifact URL. The verified package is
# therefore placed in each plane's extension cache, with the receipt that plane
# checks before it would download the archive, ahead of time. Planner
# (GATEWAY_COMPANION_PLUGIN_DIR) and every Spinner (GATEWAY_PLUGIN_DIR) then
# reconcile the activation exactly as they do in production, minus the fetch.
# scripts/docker-integration-smoke.sh prepares its Web, DNS, and Email packages
# the same way.

GATEWAY_SHELL_SMOKE_VERSION=0.0.0-smoke
GATEWAY_SHELL_SMOKE_REVISION=shell-smoke-r1

# gateway_prepare_shell_extension ROOT PLANNER_CACHE SPINNER_CACHE...
#
# Builds and archives pkg/shell with the release scripts, then installs the
# package directory into PLANNER_CACHE and each SPINNER_CACHE together with its
# receipt. Sets GATEWAY_SHELL_SMOKE_ARTIFACT_SHA256 to the archive digest the
# activation must pin.
gateway_prepare_shell_extension() {
    local root=$1 planner_cache=$2
    shift 2
    local staging cache
    staging="$(mktemp -d "${TMPDIR:-/tmp}/gateway-shell-extension.XXXXXX")"

    GOFLAGS="${GOFLAGS:--mod=mod}" GOWORK=off \
        "$root/scripts/build-gateway-automation-extension" \
        "$root/pkg/shell" "$staging/webong%2fshell" \
        "$GATEWAY_SHELL_SMOKE_VERSION" "$GATEWAY_SHELL_SMOKE_REVISION" >/dev/null
    GATEWAY_SHELL_SMOKE_ARTIFACT_SHA256="$(
        "$root/scripts/archive-gateway-extension" \
            "$staging/webong%2fshell" "$staging/webong%2fshell.tar.gz" | awk '{print $1}'
    )"
    if [[ ! "$GATEWAY_SHELL_SMOKE_ARTIFACT_SHA256" =~ ^[a-f0-9]{64}$ ]]; then
        echo 'Could not archive the Shell extension package.' >&2
        rm -rf -- "$staging"
        return 1
    fi

    for cache in "$planner_cache" "$@"; do
        mkdir -p "$cache/.desired"
        cp -Rp "$staging/webong%2fshell" "$cache/webong%2fshell"
    done
    # Planner's receipt records only the activation revision and archive digest.
    php -r '
        file_put_contents($argv[1]."/.desired/".hash("sha256", "webong/shell").".json",
            json_encode(["revision" => 1, "sha256" => $argv[2]], JSON_THROW_ON_ERROR));
    ' "$planner_cache" "$GATEWAY_SHELL_SMOKE_ARTIFACT_SHA256"
    # Spinner's receipt also names the installed package revision.
    for cache in "$@"; do
        php -r '
            file_put_contents($argv[1]."/.desired/".hash("sha256", "webong/shell").".json", json_encode([
                "extension" => "webong/shell", "version" => $argv[2], "revision" => 1,
                "artifact_sha256" => $argv[3], "package_revision" => $argv[4],
            ], JSON_THROW_ON_ERROR));
        ' "$cache" "$GATEWAY_SHELL_SMOKE_VERSION" "$GATEWAY_SHELL_SMOKE_ARTIFACT_SHA256" "$GATEWAY_SHELL_SMOKE_REVISION"
    done
    rm -rf -- "$staging"
}

# gateway_activate_shell_extension PLANNER_URL REGISTRY_TOKEN
#
# Creates the pinned `webong/shell` activation. Call it after
# gateway_prepare_shell_extension and before starting the Spinner so its first
# reconciliation already sees the package.
gateway_activate_shell_extension() {
    local planner=$1 registry_token=$2 body status
    body="$(mktemp "${TMPDIR:-/tmp}/gateway-shell-activation.XXXXXX")"
    status="$(curl -sS -o "$body" -w '%{http_code}' -X POST "$planner/extensions" \
        -H "Authorization: Bearer $registry_token" -H 'Content-Type: application/json' \
        -d "{\"extension\":\"webong/shell\",\"enabled\":true,\"version\":\"$GATEWAY_SHELL_SMOKE_VERSION\",\"artifact\":{\"url\":\"https://extensions.invalid/webong-shell-$GATEWAY_SHELL_SMOKE_VERSION.tar.gz\",\"sha256\":\"$GATEWAY_SHELL_SMOKE_ARTIFACT_SHA256\"}}")"
    if [[ "$status" != "201" ]]; then
        echo "Shell Extension activation failed: HTTP $status $(<"$body")" >&2
        rm -f -- "$body"
        return 1
    fi
    rm -f -- "$body"
}
