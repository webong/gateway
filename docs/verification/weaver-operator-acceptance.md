# Weaver operator cutover acceptance

## Scope

The operator selected a **disposable representative rehearsal**, not an existing
production copy. This gate exercises real legacy RPC, packaged cutover/rollback
CLI, installed Planner resources, Ext artifact installation and Gateway's durable
HTTP delivery on Spinner, RoadRunner Router and FrankenPHP Router. Nothing is
published or deployed. This does not certify a production provider or move a
production endpoint.

The original standalone source remains pinned to
`00e0bbcdd61c2c6ed96af55865d0ef2e77557adb`. Weaver continues consuming released
WorkFlow core `v0.1.0-rc.2`; the prepared example move is not a new dependency.

## Acceptance sequence

1. Install the original standalone schema in an isolated SQLite volume with queue
   capacity three. Start a version-1 order, execute its approval into deferred
   state, and retain the callback issued before the endpoint switch.
2. Stop the original ingress and worker. Assert the original container is stopped.
   Copy its closed DELETE-journal database into a private backup and restored copy;
   compare file hashes and run SQLite integrity checks. Start the original app
   against the restored copy, verify the waiting public state, then stop it.
   Production WAL databases require a coherent SQLite backup/WAL plan; this fixture
   deliberately makes no live file-copy claim.
3. Use a separate reference copy and the original queue implementation to complete
   approval and reserve the dependent HTTP claim. Retain that old implementation's
   effect key without executing it. The original source and backup stay unchanged.
4. Copy into a fresh Weaver database using the packaged CLI and explicit account
   assignment. Refuse a second copy into the existing target. Compare queue runs,
   claims, outbox and the complete capacity/coordination record before activation.
   Provision the database file ACL for the actual host runtime identity.
5. Install from empty private caches over the disposable HTTPS fixture. Activate
   Weaver through installed Planner resources selecting definition version 2.
   Verify the copied public state and physical execution ID still reference
   version 1, and reject the old standalone credential.
6. Restart the actual host before completing the old approval callback. Submit it
   twice. Let Gateway perform the dependent HTTP request: the provider commits one
   effect and loses its first response; the retry must use the original queue's
   effect key. Require two attempts, one provider ledger row and no version-2 step.
7. Drain claims and work notifications. Disable Weaver and wait for its supervised
   children to stop before rollback. Roll current progress into a fresh legacy
   database, preserving queue coordination revision as well as capacity.
8. Start the original app against the rollback database. Verify completed progress,
   replay the old callback, and prove that no worker claim or provider effect is
   created. A legitimate legacy read/replay may advance its local coordination
   counter; it must not regress or alter capacity. The original source file must
   still match its pre-cutover hash.

No production data, active-run authority or old release is changed. The exercise
contains only imported runs; the separate refusal tests still block automatic
legacy rollback when Weaver-native or unrouted runs exist.

## Run and inspect

Build the pinned legacy source, Weaver package and actual host images with the
existing runtime-gate scripts. On a native runner:

```sh
python3 tools/workflow-runtime-gate/host-matrix.py spinner gateway-weaver:arm64 --fresh-https --operator-cutover --operator-report /private/operator-spinner.json
python3 tools/workflow-runtime-gate/host-matrix.py roadrunner gateway-weaver:arm64 --fresh-https --operator-cutover --operator-report /private/operator-roadrunner.json
python3 tools/workflow-runtime-gate/host-matrix.py frankenphp gateway-weaver:arm64 --fresh-https --operator-cutover --operator-report /private/operator-frankenphp.json
```

Use `amd64` on that native runner. The legacy image defaults to
`gateway-workflow-runtime-gate:source-ARCH`; `--legacy-image` may select an existing
local tag only when its source label matches the pinned commit. Report parent
folders must exist; existing reports are never overwritten. Reports are mode
0600 and contain source/backup hashes, package identity, provider receipt/counts,
coverage and explicit `production_migrated: false` / `published: false` flags.
They contain no credentials. Networks, containers and all rehearsal volumes are
removed afterward; copy no test database into a production location.

The representative actual-host dataset uses SQLite. The separate mandatory
SQLite/PostgreSQL/MySQL suite verifies same-engine copy and rollback, source
read-only behavior, claims/reservation refusals and record preservation with real
services and no missing-service skips. Neither test substitutes for accepting a
specific operator's SQL, backup system, credentials or provider guarantee.

## Local verification

On 2026-11-15 the disposable rehearsal passed on native Linux arm64 across all
three actual hosts. Each report retained queue capacity 3 and source coordination
revision 4, matching backup hashes and one provider effect from two requests.
The mandatory SQLite/PostgreSQL/MySQL suite passed 142 tests / 800 assertions.
These are local results; the new CI steps still require remote execution on both
architectures. No runtime artifacts have been published.

## Remaining production decisions

An operator-specific acceptance requires the chosen immutable backup, verified
restore, read-only source access, audited MySQL identities where applicable,
explicit account/grant mapping, provider deduplication scope and uncertainty
reconciliation, runtime ACLs, drain evidence, endpoint rollback route and retained
legacy image. Actual cutover/deployment and publication remain unapproved.

The old WorkFlow host can be prepared under `examples/standalone` with compatible
`app/` entrypoints while this work proceeds. That source relocation does not retire
compatibility, rewrite an active database, or require an unreleased core. Removing
compatibility remains a later versioned, explicitly approved decision.
