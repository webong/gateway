# Standalone Gateway preview release preparation

## Scope

The initial product is Gateway as its own service, extended through installed
packages. RoadRunner is the default image; FrankenPHP is the alternative.
Existing-application PHP integration is outside this preview's support promise.
No tag, image, archive, metadata or release is published by this preparation.

The preview covers HTTP/webhook delivery, WebSockets and webstreams, management
API/CLI, local persistent state, and extension activation. Email, DNS, automations
and managed realtime services require their own explicit extension support matrix;
their presence in the repository is not acceptance for every deployment shape.
Weaver publication, Workload adoption (#31), and Spatie extraction (#20) are
separate work. Do not remove or migrate their compatibility surfaces as part of
this preview.

## Reproducible acceptance

Use the production Dockerfile for both runtimes:

```sh
docker build --build-arg GATEWAY_ROUTER_RUNTIME=roadrunner -f infra/docker/Gateway.router.Dockerfile -t gateway-preview:roadrunner .
docker build --build-arg GATEWAY_ROUTER_RUNTIME=frankenphp -f infra/docker/Gateway.router.Dockerfile -t gateway-preview:frankenphp .
```

Build Web for the target Linux architecture with
`scripts/build-gateway-runtime-extension`. The package must be readable by the
Router's runtime user. On an amd64 host build amd64; on arm64 build arm64.
Then run, substituting the other runtime for the alternative image:

```sh
python3 tools/release-acceptance/standalone.py gateway-preview:roadrunner --web-package /absolute/extensions/webong%2fweb --report /private/standalone.json
bash scripts/test-router-managed-route gateway-preview:roadrunner
GATEWAY_ROUTER_SKIP_BUILD=true GATEWAY_ROUTER_IMAGE=gateway-preview:roadrunner bash scripts/test-router-companion-image
bash scripts/test-publish-gateway-image
```

The standalone check uses generated credentials, an isolated HTTPS receiver and
private Docker volumes. TLS verification and destination address checks remain
active. It creates the documented endpoint/subscription with the operator CLI,
checks management authentication/private route isolation, delivers a real
webhook, installs a separately built Web package, relays SSE including
`Last-Event-ID`, recovers a pending delivery after container replacement, and
restores an immutable stopped-volume
backup into a fresh volume. It verifies retained credentials, registry and Web
activation, and no completed webhook replay. Reports contain image identity,
architecture, snapshot digest and explicit non-publication/non-production flags;
they never contain credentials. Existing reports are not overwritten.

The managed-route smoke separately checks packaged HTTP/WebSocket proxying,
private-route isolation and stale/destination refusal. Its instance records are
fixtures: it does not certify process provisioning or Workload adoption.
The companion smoke builds an extension after the host image, activates it,
executes it, disables/reactivates it and rejects package tampering.

The manual `Standalone Gateway acceptance` workflow repeats these checks on
native Linux amd64 and arm64 for both runtimes. It has read-only permissions,
loads images only into its runner, and performs no uploads or publication.

## Local results — 2026-11-16

Both production runtime variants passed on Linux arm64. The first clean
RoadRunner trial exposed a missing core HTTP fallback: the documented webhook
returned 503 without an installed traffic extension. A focused regression failed
before the fix; RoadRunner now uses the same extension-first/core-HTTP-fallback
composition as FrankenPHP. Its full race-enabled test suite passed, as did the
shared Router, extension registry and durable queue suites.

Both images passed fresh HTTPS webhook delivery, installed SSE/Last-Event-ID,
deferred-delivery recovery after replacement, immutable stopped-volume restore,
credential/registry/activation retention, and no completed replay. Both also
passed packaged HTTP/WebSocket routing and separately built companion activation,
execution, disable/reactivate and integrity rejection. The four-job native CI
run remains the required evidence for the committed candidate.

## Release gates still required

- Run the new four-job workflow against the committed candidate and retain the
  run URL and source SHA. Local arm64 success does not establish native amd64.
- Confirm the final version and support matrix. Existing v0.1.0/v0.1.1 releases
  describe guest packages; do not repurpose or mutate those tags.
- Accept the selected immutable image and extension archives together. Test their
  fresh HTTPS download/installation at the eventual public origin after separate
  publication approval; private fixtures do not certify the CDN or operator trust.
- Finalize [draft release notes](../releases/standalone-preview.md) with the
  approved version, enabled capabilities and exact extension versions.
- Cross-version upgrade/schema rollback remains unverified. This rehearsal proves
  same-version container replacement and stopped-volume restore. Preserve the
  full data directory and matching old image; pending/uncertain deliveries need
  reconciliation before rollback. Do not promise downgrade compatibility.
- SQLite plus local spool is the default. Registry PostgreSQL/MySQL and shared
  PostgreSQL deliveries have separate support/tests; shared MySQL deliveries are
  unsupported. The rehearsal does not certify distributed/multi-node operation.
- Real public DNS/SMTP operation and external provider guarantees remain separate
  acceptance if those extensions are included in the release promise.

## Publication controls

`scripts/publish-gateway-image VERSION` now prints a dry-run command by default.
Actual publication needs `--push` and a clean tree. Alternative images receive
`VERSION-frankenphp`; they cannot replace the RoadRunner `latest`. Moving
`latest` needs `--latest` and a stable RoadRunner version; prereleases and SHA
builds never update it. Image labels record the source commit and version.
The control regression uses a fake Docker command and never publishes.

A global `v*` Git tag triggers Composer package publication. A published global
GitHub release triggers guest asset publication. Those actions are separate
from image publication and must not occur until their exact scope is approved.
Do not run the publisher with `--push`, create tags/releases or enable Weaver's
artifact-upload approval while publication is prohibited.
