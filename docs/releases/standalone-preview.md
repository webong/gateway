# Gateway standalone preview — draft release notes

Status: preparation only. Version and tested source commit are not yet frozen.
No release, image or extension package has been published from this candidate.

Gateway is a programmable gateway for WWW, SMTP, DNS and others. This preview
starts with Gateway as an independently deployed service, extended by installed
packages. RoadRunner is the default Router image; FrankenPHP is the alternative.

## Candidate acceptance

The release candidate must pass on native Linux amd64 and arm64:

- Fresh startup, generated management credentials and the documented CLI.
- Real HTTPS webhook delivery and durable recovery after a receiver failure.
- Installed Web extension streaming, including SSE reconnection identifiers.
- Packaged HTTP/WebSocket routing and private-route/destination isolation.
- Custom extension activation, execution, disable/reactivate and tamper rejection.
- Same-version container replacement and stopped-volume backup/restore, retaining
  credentials, subscriptions and extension activation.

The support promise for each optional extension must be listed with its exact
version before these notes are finalized. WWW acceptance is described above.
DNS, Email, automation languages and managed service provisioning require their
own deployment-specific acceptance; they are not implied by the WWW checks.
Weaver remains separately unpublished.

## Install and operate

Follow [Set up and use Gateway](../using-gateway.md) for the Docker setup,
management token and first webhook. Replace `latest` with the final versioned
image when the release is approved. See [Router deployment](../router.md) for
configuration and [extensions](../planner-extensions.md) for adding capabilities.

Persist `/data`. For the default SQLite/local-spool deployment, stop Gateway
before backing up the complete volume. The backup includes credentials, database,
delivery state and installed packages; protect it as sensitive operator data.
Record the matching image digest and externally mounted extension versions.

## Limits

- Existing-app PHP embedding is outside this initial standalone support scope.
- Default storage is SQLite plus the local delivery spool. Shared deliveries
  require PostgreSQL; MySQL shared deliveries are unsupported.
- Same-version restore is tested. Cross-version schema downgrade and automatic
  reconciliation of external side effects are not promised. A restored backup
  may contain work performed after that backup was taken: reconcile uncertain or
  pending effects before resuming it.
- Managed-route proxy tests use fixture instance records. They do not establish
  controller recovery/fencing or complete Workload migration.
- No public-origin installation, production cutover or external provider
  guarantee has been certified by a disposable local test.

Publication remains a separate approval. A global version tag can also trigger
Composer publishing; publishing a global GitHub release can publish guest assets.
See the [release preparation record](../verification/standalone-preview-release.md)
for the exact gates and non-publishing workflow.
