#!/usr/bin/env bash

# Installs an automation engine (Shell, Lua, ...) into a disposable Planner and
# Spinner the way a Gateway deployment does: build the `webong/<engine>`
# package, archive it, and activate it through Planner's `/extensions` resource.
# No Gateway host binary compiles a concrete automation engine, so a Spinner or
# bundled Router only runs that engine's hooks and schedules after this
# activation has been reconciled.
#
# The one step a loopback smoke cannot perform is the HTTPS download: Planner
# and Spinner both require an `https` artifact URL. The verified package is
# therefore placed in each plane's extension cache, with the receipt that plane
# checks before it would download the archive, ahead of time. Planner
# (GATEWAY_COMPANION_PLUGIN_DIR) and every Spinner (GATEWAY_PLUGIN_DIR) then
# reconcile the activation exactly as they do in production, minus the fetch.
# scripts/docker-integration-smoke.sh prepares its Web, DNS, and Email packages
# the same way.

GATEWAY_AUTOMATION_SMOKE_VERSION=0.0.0-smoke

# gateway_prepare_automation_extension ROOT ENGINE PLANNER_CACHE SPINNER_CACHE...
#
# Builds and archives pkg/ENGINE (for example `shell` or `lua`) with the release
# scripts, then installs the package directory into PLANNER_CACHE and each
# SPINNER_CACHE together with its receipt. Records the archive digest the
# activation must pin for gateway_activate_automation_extension.
gateway_prepare_automation_extension() {
    local root=$1 engine=$2 planner_cache=$3
    shift 3
    local revision="$engine-smoke-r1" staging cache sha
    if [[ ! "$engine" =~ ^[a-z]+$ ]]; then
        echo "Unsupported automation engine name: $engine" >&2
        return 1
    fi
    staging="$(mktemp -d "${TMPDIR:-/tmp}/gateway-$engine-extension.XXXXXX")"

    GOFLAGS="${GOFLAGS:--mod=mod}" GOWORK=off \
        "$root/scripts/build-gateway-automation-extension" \
        "$root/pkg/$engine" "$staging/webong%2f$engine" \
        "$GATEWAY_AUTOMATION_SMOKE_VERSION" "$revision" >/dev/null
    sha="$(
        "$root/scripts/archive-gateway-extension" \
            "$staging/webong%2f$engine" "$staging/webong%2f$engine.tar.gz" | awk '{print $1}'
    )"
    if [[ ! "$sha" =~ ^[a-f0-9]{64}$ ]]; then
        echo "Could not archive the $engine extension package." >&2
        rm -rf -- "$staging"
        return 1
    fi
    printf -v "GATEWAY_AUTOMATION_SMOKE_SHA256_$engine" '%s' "$sha"

    for cache in "$planner_cache" "$@"; do
        mkdir -p "$cache/.desired"
        cp -Rp "$staging/webong%2f$engine" "$cache/webong%2f$engine"
    done
    # Planner's receipt records only the activation revision and archive digest.
    php -r '
        file_put_contents($argv[1]."/.desired/".hash("sha256", $argv[2]).".json",
            json_encode(["revision" => 1, "sha256" => $argv[3]], JSON_THROW_ON_ERROR));
    ' "$planner_cache" "webong/$engine" "$sha"
    # Spinner's receipt also names the installed package revision.
    for cache in "$@"; do
        php -r '
            file_put_contents($argv[1]."/.desired/".hash("sha256", $argv[2]).".json", json_encode([
                "extension" => $argv[2], "version" => $argv[3], "revision" => 1,
                "artifact_sha256" => $argv[4], "package_revision" => $argv[5],
            ], JSON_THROW_ON_ERROR));
        ' "$cache" "webong/$engine" "$GATEWAY_AUTOMATION_SMOKE_VERSION" "$sha" "$revision"
    done
    rm -rf -- "$staging"
}

# gateway_activate_automation_extension ENGINE PLANNER_URL REGISTRY_TOKEN
#
# Creates the pinned `webong/ENGINE` activation. Call it after
# gateway_prepare_automation_extension. A standalone Spinner started afterwards
# sees the package on its first reconciliation; a bundled Router, whose Planner
# only exists once it is running, picks the activation up on its next
# reconciliation tick (every 15 seconds).
gateway_activate_automation_extension() {
    local engine=$1 planner=$2 registry_token=$3 sha_var body status
    sha_var="GATEWAY_AUTOMATION_SMOKE_SHA256_$engine"
    body="$(mktemp "${TMPDIR:-/tmp}/gateway-$engine-activation.XXXXXX")"
    status="$(curl -sS -o "$body" -w '%{http_code}' -X POST "$planner/extensions" \
        -H "Authorization: Bearer $registry_token" -H 'Content-Type: application/json' \
        -d "{\"extension\":\"webong/$engine\",\"enabled\":true,\"version\":\"$GATEWAY_AUTOMATION_SMOKE_VERSION\",\"artifact\":{\"url\":\"https://extensions.invalid/webong-$engine-$GATEWAY_AUTOMATION_SMOKE_VERSION.tar.gz\",\"sha256\":\"${!sha_var}\"}}")"
    if [[ "$status" != "201" ]]; then
        echo "$engine Extension activation failed: HTTP $status $(<"$body")" >&2
        rm -f -- "$body"
        return 1
    fi
    rm -f -- "$body"
}
