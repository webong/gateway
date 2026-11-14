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
core projection. The installed Planner provider and resource tests pass locally.
The full extension setup remains incomplete.

## Runtime decision (gates 1–2)

Review the runtime/ownership ADR and PR #24 with their native host evidence.
**Do not merge PR #24 until the full extension setup is complete.** There is no
separate early merge gate before extraction.

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
  tests. Production-secret and unavailable-storage rehearsals remain required.

## Delivery, failure coverage and SQL acceptance (gate 5)

- Implemented: exact durable-idempotent and own-process adapter-readiness
  capabilities, stable outbox receipt IDs, fenced acknowledgement after durable
  acceptance, opaque-claim delivery and the atomic execution gate. Transient
  enqueue/control/ack failures retry without activation changes. Real business
  provider operations remain to be deployed and accepted.
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
  final package, with upgrade/replacement and failure cases. The runtime harness
  preloads verified artifact caches and uses disposable tokens; fresh HTTPS
  download/installation and production trust policy need separate acceptance.

## Cutover and rollback rehearsal (gate 6)

- Rehearse on copied standalone data: drain claims, reconcile uncertain effects,
  migrate routing reservations and queue records, switch the endpoint and accept
  callbacks issued before cutover.
- Assert pinned definitions/context, logical/scoped identities, attempts, callback
  receipts, outbox IDs and effect-idempotency keys survive. Audit the MySQL identity
  upgrade where required.
- Rehearse rollback and document schema/version compatibility and the point beyond
  which rollback needs reconciliation. Never run old and new active writers on the
  same executions, or silently convert inline/queue/Temporal authority.

## Release and deprecation (gate 7)

- Publish reproducible Linux amd64/arm64 extension artifacts, Composer metadata and
  checksums through the reviewed release process; validate publisher/artifact trust.
- Smoke-test fresh installation from published artifacts and ship approval/order
  examples plus installation, authentication, upgrade, operation and rollback docs.
- Once replacement acceptance passes, move WorkFlow `app/` into examples for
  testing/reference, or deprecate it with versioned compatibility guidance. It
  should not remain a second supported production host. Keep the engine and
  authoring CLI independent; do not remove legacy source before replacement.

No production migration, artifact publication, old-app deletion or automatic
run-authority conversion is part of the runtime/host proof.
