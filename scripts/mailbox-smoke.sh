#!/usr/bin/env bash
set -euo pipefail

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="${script_dir}/.."
mailbox_image="${GATEWAY_MAILBOX_SMOKE_IMAGE:-dovecot/dovecot:latest-dev}"
smoke_password="mailbox-smoke-pass"
temp_dir="$(mktemp -d)"
imap_id=""
pop3_id=""

cleanup() {
  if [[ -n "$imap_id" ]]; then docker stop "$imap_id" >/dev/null 2>&1 || true; fi
  if [[ -n "$pop3_id" ]]; then docker stop "$pop3_id" >/dev/null 2>&1 || true; fi
  rm -f "${temp_dir}/imap-ca.pem" "${temp_dir}/pop3-ca.pem"
  rmdir "$temp_dir"
}
trap cleanup EXIT

cd "$repo_root"
imap_id="$(docker run --rm -d -p 127.0.0.1::31993 -e "USER_PASSWORD=${smoke_password}" "$mailbox_image")"
pop3_id="$(docker run --rm -d -p 127.0.0.1::31995 -e "USER_PASSWORD=${smoke_password}" \
  -v "${repo_root}/infra/docker/dovecot-pop3-test.conf:/etc/dovecot/conf.d/pop3.conf:ro" "$mailbox_image")"
docker cp -L "${imap_id}:/etc/dovecot/ssl/tls.crt" "${temp_dir}/imap-ca.pem"
docker cp -L "${pop3_id}:/etc/dovecot/ssl/tls.crt" "${temp_dir}/pop3-ca.pem"
imap_addr="$(docker port "$imap_id" 31993/tcp)"
pop3_addr="$(docker port "$pop3_id" 31995/tcp)"

GATEWAY_TEST_IMAP_UPSTREAM_ADDR="$imap_addr" \
GATEWAY_TEST_IMAP_CA_FILE="${temp_dir}/imap-ca.pem" \
GATEWAY_TEST_IMAP_UPSTREAM_PASSWORD="$smoke_password" \
  GOWORK=off go -C pkg/email test -mod=mod -count=1 -run '^TestExternalIMAPInteroperability$' ./spinner/imap

GATEWAY_TEST_POP3_UPSTREAM_ADDR="$pop3_addr" \
GATEWAY_TEST_POP3_CA_FILE="${temp_dir}/pop3-ca.pem" \
GATEWAY_TEST_POP3_UPSTREAM_PASSWORD="$smoke_password" \
  GOWORK=off go -C pkg/email test -mod=mod -count=1 -run '^TestExternalPOP3Interoperability$' ./spinner/pop3
