# WorkFlow extension migration: work remaining

Tracking issue: [#18](https://github.com/webong/gateway/issues/18).
Runtime prototype and host acceptance: [PR #24](https://github.com/webong/gateway/pull/24).

The original feasibility tool remains under `tools/workflow-runtime-gate`; the
actual extension is now **Weaver**, under `pkg/weaver`, with identity
`webong/gateway-weaver` and namespace `Webong\Gateway\Weaver`. The user chose the
name on 2026-11-14. The engine stays `webong/workflow`.

The production Spinner entrypoint, bridge, worker, application stores, explicit
schema installer and account-scoped runtime authorization have been extracted.
The dependency now uses released WorkFlow v0.1.0-rc.2 at
`278861082e00537fe7e759eb7c8bee3e7a3f0bf4`, installed as a dist with a root-owned
core projection. The installed Planner provider and resource tests pass, including exact Composer
TAR installation.
PR #24 was merged into main at `543e8d500195625d802125177790a2f8ffad8fd0`.
Source integration is complete; publication and operator replacement acceptance
remain separate and incomplete. The owner explicitly prohibits publication.

## Runtime decision (gates 1–2)

Completed: the owner approved source integration after the native host proof.
PR #24 is merged. Its final native amd64/arm64 run passed all three hosts, the
extracted SQL suite (142 tests / 794 assertions) and Composer dist acceptance.
This does not authorize publishing, deployment or old-app removal.

## Dependency and extraction (gate 3)

- Completed: compatible independent core v0.1.0-rc.2, tagged dist consumption,
  production `core` projection and separate compatibility-test surface union.
  Both consumer lockfiles and projection locks are committed with the consumer.
- Completed: extracted application RPC facade, account/subject identity, PDO stores,
  worker, bridge and tests live under Weaver, separate from the engine.
- Build and install it from a clean external consumer. Verify Composer-only core,
  optional adapters and native builds remain independent of Gateway. Preserve the
  old application until the replacement has been released.

## Planner resources, permissions and operational storage (gate 4)

- Implemented: Composer provider discovery, immutable account-owned definition
  versions, approved operation catalog, management routes, conditional grant
  revisions and explicit account/flow/subject/action permissions.
- Implemented: Planner resource projection into runtime snapshots, explicit
  operational installation, scoped database/authentication secret references and
  rejection of invalid/cross-account selections. Credentials and queue state stay
  outside desired resource documents.
- Exercise the installed Planner entrypoint with a separately deployed Planner,
  including denied scopes, invalid configuration, secret rotation/revocation and
  unavailable storage. The installed host matrix seeds through WeaverResources and consumes the
  real provider projection. Operator HTTP boundaries have separate installed-app
  tests. Credential rotation, live grant revocation, invalid projection and unavailable
  storage rejection now pass on the actual local three-host matrix. Native CI
  reruns the same acceptance for both architectures.

## Delivery, failure coverage and SQL acceptance (gate 5)

- Implemented: exact durable-idempotent and own-process adapter-readiness
  capabilities, stable outbox receipt IDs, fenced acknowledgement after durable
  acceptance, opaque-claim delivery and the atomic execution gate. Transient
  enqueue/control/ack failures retry without activation changes. `gateway.http.v1` now uses an operator-secret destination, pinned method/URL/
  provider deduplication scope/header and a no-redirect host capability. Real local
  host acceptance proves one effect after a lost response, non-idempotent fencing
  and receipt preservation across credential rotation. Production acceptance remains
  operator-owned.
- Test absent, inactive, ambiguous and incompatible bindings; publication crashes,
  uncertain provider effects, stale completion/heartbeat fencing, backoff/deadlines,
  cancellation races and authenticated callback correlation across replacement.
  Retain explicit idempotency policies and reconciliation for uncertain
  non-idempotent attempts.
- Maintain the extracted PHP 8.3 queue/RPC integration suite on SQLite, PostgreSQL and
  MySQL with real services and no service-related skips. Bundled PDO presence and
  the SQLite host fixture do not establish PostgreSQL/MySQL application acceptance.
  The upstream SQL prerequisite is evidence to preserve, not a substitute for
  testing the extracted package.
- Keep all three production hosts and both native architectures green against the
  final package, with upgrade/replacement and failure cases. `--fresh-https` now
  starts with empty Planner/Spinner artifact caches and uses a private TLS fixture.
  It covers checksum rejection, explicit CA trust and HTTPS redirects through the
  actual downloaders, followed by the existing workflow/failure acceptance.
  This private rehearsal does not establish published-origin installation.

## Cutover and rollback rehearsal (gate 6)

- Implemented and rehearsed: read-only standalone source -> fresh same-engine
  destination, explicit account assignments/imported routing markers and unchanged
  queue/claim/outbox records. SQLite, PostgreSQL and MySQL round trips preserve
  original execution IDs, pinned definitions/context, attempts and callback/effect
  idempotency inputs. MySQL requires audited binary identities.
- Real native RPC endpoint rehearsal stops the old writer, runs the packaged CLI,
  switches to Weaver under new credentials, accepts an old callback and replays it,
  then stops Weaver and rolls current progress into a fresh legacy database.
- Regression guards reject active claims/reservations, wrong imported scopes,
  missing queue reinitialization, existing destinations and unsupported authority.
  Cross-engine migration is unsupported. Weaver-native runs/unrouted records stop
  automatic legacy rollback rather than changing authority.
- Representative operator acceptance now covers stopped-writer backup/restore,
  real three-host activation, old callbacks, original effect keys, one HTTP effect
  after a lost response and current-progress legacy rollback. The actual-host
  dataset is SQLite; the independent mandatory SQL suite covers all three engines.
  Reports explicitly mark the dataset disposable and production unmigrated.
  See `weaver-operator-acceptance.md`.
- The operator still owns a production-specific backup/drain/reconciliation plan.
  See `pkg/weaver/UPGRADE.md`; no production data was accessed or migrated.

## Release and deprecation (gate 7)

- Candidate preparation now checks manifest/file integrity, shared versions,
  runtime/Composer source parity and deterministic archives. The exact Composer
  TAR installs into a real isolated Planner consumer.
- Builds pin the core runtime and bridge/static-entrypoint builder by OCI digest.
  The original arm64 cold-cache rebuild matched its local candidate. Explicit gzip
  compression now also reproduces the prior native CI runtime/Composer checksums
  across the local and CI Python defaults. New acceptance runs use a private HTTPS
  Composer index/dist, reject untrusted TLS and corrupt downloads, and keep the
  Composer cache empty.
- Runtime release scope: Linux amd64/arm64 extension archives, checksums and
  Gateway installation metadata. Separate Composer package/index publication is
  not required. Direct PHP consumers can use Composer Source Plugin against a
  pinned source checkout; existing Composer dist fixtures remain internal tests.
- Blocked by owner instruction: publishing runtime archives, installation metadata,
  checksums or tags. Do not ask for publication again unless the owner changes
  that instruction. Published-origin/trust acceptance remains open.
- Smoke-test fresh Gateway installation from the published runtime archives
  using Gateway installation metadata; no public Composer index is required. Ship
  approval/order examples plus installation, authentication, upgrade, operation and
  rollback docs.
- The earlier local WorkFlow example-host preparation is not present in the
  inspected clean main checkout at `3f037f5`: `app/` remains and
  `examples/standalone` is absent. Locate or prepare that move before claiming it
  complete. Preserve compatible entrypoints/namespace and engine/CLI independence.
- Once production replacement acceptance passes, retire compatibility only with
  an explicit version boundary and approval. The example host remains available for
  testing/reference. It should not remain a second supported production host.
  Keep the engine and
  authoring CLI independent; do not remove legacy source before replacement.

No production migration, artifact publication, old-app deletion or automatic
run-authority conversion is part of the runtime/host proof.
