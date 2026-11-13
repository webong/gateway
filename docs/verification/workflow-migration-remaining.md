# WorkFlow extension migration: work remaining

Tracking issue: [#18](https://github.com/webong/gateway/issues/18).
Runtime prototype and host acceptance: [PR #24](https://github.com/webong/gateway/pull/24).

The runtime proof is intentionally under `tools/workflow-runtime-gate`, not the
production `pkg/workflow` package. Passing it establishes that the packaged PHP
bridge and queue worker can be hosted; it does not complete extraction,
authorization, delivery integration or cutover.

## Runtime decision (gates 1–2)

Review and land the proposed runtime/ownership ADR and PR #24 with the linked
native host evidence. This settles the bundled PHP runtime support boundary; it
does not make the feasibility fixture a production package.

## Dependency and extraction (gate 3)

- Release a compatible independent `webong/workflow` core containing the required
  queue contracts. The only published release checked on 2026-11-14 was
  `v0.1.0-rc.1`, which predates them. Consume a released version through Composer,
  not a path repository or copied vendor tree.
- Create `pkg/workflow` as `webong/gateway-workflow`, with the
  `Webong\Gateway\Workflow` namespace and one shared Planner/Spinner identity and
  version. Move the application RPC facade, scoped identity/routing adapter,
  PDO stores, worker/bootstrap code and their tests according to the inventory.
- Build and install it from a clean external consumer. Verify Composer-only core,
  optional adapters and native builds remain independent of Gateway. Preserve the
  old application until the replacement has been released.

## Planner resources, permissions and operational storage (gate 4)

- Implement the actual Planner entrypoint: immutable definition versions, approved
  operation catalog and configuration, management routes and explicit grants for
  each resource/run-control action in its account/subject scope.
- Replace fixed trusted test definitions and `/state` bootstrap with supported
  operational configuration, independently installable migrations and scoped
  database/provider secret references. Keep credentials outside revisioned
  resources and queue state outside Planner desired state.
- Exercise the installed Planner entrypoint with a separately deployed Planner,
  including denied scopes, invalid configuration, secret rotation/revocation and
  unavailable storage. The host-matrix CLI only seeds disposable production stores;
  it is not this production management/authorization implementation.

## Delivery, failure coverage and SQL acceptance (gate 5)

- Bind durable delivery through an exact Arranger capability: persistent outbox
  event ID → opaque claim ID → queue execution gate → provider effect. Preserve
  independent Gateway delivery leases and WorkFlow attempt leases.
- Test absent, inactive, ambiguous and incompatible bindings; publication crashes,
  uncertain provider effects, stale completion/heartbeat fencing, backoff/deadlines,
  cancellation races and authenticated callback correlation across replacement.
  Retain explicit idempotency policies and reconciliation for uncertain
  non-idempotent attempts.
- Run the extracted PHP 8.3 queue/RPC integration suite on SQLite, PostgreSQL and
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
- Deprecate WorkFlow `app/` with versioned compatibility guidance. Remove it only in
  a documented later release once the replacement is available.

No production migration, artifact publication, old-app deletion or automatic
run-authority conversion is part of the runtime/host proof.
