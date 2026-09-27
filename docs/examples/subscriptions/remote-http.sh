#!/usr/bin/env bash

set -euo pipefail

: "${REGISTRY_BASE_URL:?Set REGISTRY_BASE_URL, for example https://relay.example.test}"
: "${REGISTRY_TOKEN:?Set REGISTRY_TOKEN}"
: "${ENDPOINT_KEY:?Set ENDPOINT_KEY to the resolved endpoint key}"

example_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
subscriptions_url="${REGISTRY_BASE_URL%/}/registry/endpoints/${ENDPOINT_KEY}/subscriptions"

# Remote subscription using Spinner's default outbound network configuration.
curl --fail-with-body --silent --show-error \
  --include \
  --request POST "$subscriptions_url" \
  --header "Authorization: Bearer ${REGISTRY_TOKEN}" \
  --header 'Content-Type: application/json' \
  --data-binary "@${example_dir}/remote-direct.json"
printf '\n'

# Remote subscription selecting the endpoint's existing `proxy` profile.
curl --fail-with-body --silent --show-error \
  --include \
  --request POST "$subscriptions_url" \
  --header "Authorization: Bearer ${REGISTRY_TOKEN}" \
  --header 'Content-Type: application/json' \
  --data-binary "@${example_dir}/remote-profile.json"
printf '\n'

# Optional lifecycle examples. Copy id and ETag from the profiled creation
# response into these variables. If they are absent, creation is all we run.
if [[ -n "${PROFILE_DESTINATION_ID:-}" && -n "${PROFILE_ETAG:-}" ]]; then
  # Select another active endpoint profile.
  curl --fail-with-body --silent --show-error \
    --include \
    --request PATCH "${subscriptions_url}/${PROFILE_DESTINATION_ID}" \
    --header "Authorization: Bearer ${REGISTRY_TOKEN}" \
    --header "If-Match: ${PROFILE_ETAG}" \
    --header 'Content-Type: application/json' \
    --data '{"profile":"backup-proxy"}'
  printf '\n'

  # Use the new ETag returned above when removing the profile. This command is
  # shown but intentionally not executed with the now-stale PROFILE_ETAG:
  printf '%s\n' \
    "PATCH ${subscriptions_url}/${PROFILE_DESTINATION_ID}" \
    'If-Match: <ETag returned by the preceding PATCH>' \
    '{"profile":null}'
fi
