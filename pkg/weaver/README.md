# Weaver

Weaver hosts the independent `webong/workflow` engine as a Gateway Extension.
Its identity is `webong/gateway-weaver`, PHP namespace is
`Webong\Gateway\Weaver`, and private control route remains `/workflow/rpc`.
Existing JSON-RPC method names (`flow.start/get/complete/resume/cancel`) and
logical run IDs are preserved.

**Development status:** Weaver owns the Spinner runtime, bridge, worker, queue/RPC
stores, explicit operational installer, account grants and Planner resource
provider. Production uses the released `webong/workflow:0.1.0-rc.2` dist at
`278861082e00537fe7e759eb7c8bee3e7a3f0bf4`, projected to the `core` surface.
The runtime and SQL/host acceptance are under active validation. Copied-data
cutover/rollback and reproducible release/installation acceptance remain open.
PR #24 stays unmerged until the full extension setup is complete.

## Runtime package

The development artifact contains a verified static Arranger entrypoint, PHP
8.3 CLI with SQLite/PostgreSQL/MySQL PDO and pcntl, the embedded PHP bridge and
its ELF/library closure. Neither the guest nor bridge imports Spinner or Router
implementations. `cmd/bridge` is a separate Go module because it needs PHP embed
headers; the main Go module remains independently testable.

After building the pinned source runtime with
`tools/workflow-runtime-gate/build.sh CLEAN_WORKFLOW_SOURCE arm64`:

```sh
pkg/weaver/build.sh arm64
pkg/weaver/test.sh
```

Use `amd64` on a native Linux amd64 runner. Set `WORKFLOW_TEST_BASE` to the pinned
source image when its tag differs from the local default. The test script creates
isolated PostgreSQL/MySQL services, requires every engine with no skips, exercises
the real bridge/worker, and removes its own containers, network and volumes.
Nothing is published by these commands.

## Configuration and credentials

The configuration shape is illustrated by
[`examples/approval-order.configuration.json`](examples/approval-order.configuration.json).
It declares secret references, approved operation keys, immutable definition
versions and explicit account/flow/subject/action grants. It contains no
credentials, commands, executable paths or uploaded PHP classes.

Two identity-scoped secrets are required:

- `database_ref`: an object containing PDO `dsn` and optional `username/password`.
  The original DSN, including PostgreSQL TLS settings, is preserved.
- `authentication_ref`: an object mapping configured account IDs to distinct
  bearer tokens of at least 32 bytes. Account ownership alone grants no action.

The supervisor writes their resolved values and the approved configuration into
a private temporary context outside the verified package, with directory mode
0700 and file mode 0600. Children receive only an explicit environment allowlist.
Removal, rotation, version change or failure of either secret stops ingress and
new claims. Reapply activation after installing the new secret revision.

Private HTTP clients send the host token in `Authorization` and the account's
Weaver credential in `X-Gateway-Extension-Authorization`. The host consumes its
credential and forwards only the Weaver credential. Transport authentication
supplies the account; RPC parameters cannot supply or replace it. Storage identity
includes account plus subject/flow/run, while the public logical run ID stays intact.

The initial deployed operation catalog contains `gateway.approval` (deferred) and
`gateway.complete` (no external effect). Adding real business/provider effects
requires a deployed executor and the reviewed durable-delivery/idempotency path;
clients cannot turn a class, command or URL into executable behavior.

## Planner installation and desired resources

The effective consumer root must require `webong/gateway-weaver`, configure the
WorkFlow VCS repository, trust `webong/composer-source-plugin` and select the
`core` projection. Keep both `composer.lock` and `composer-source.lock`.
The root-owned policy and two-pass locked installation are demonstrated in
`planner-test.Dockerfile`; production artifact installation is in `Dockerfile`.
The test consumer selects `core`, `open-workflow` and `temporal` for compatibility
coverage, without widening the production closure.

Composer's `register.php` entrypoint registers `WeaverServiceProvider` when a
Gateway Planner is present. Operator-authenticated routes publish immutable
account-owned definitions and create/replace/revoke explicit run grants under
`/weaver/accounts/{account}`. Grant updates require `If-Match`. Activation selects
published definition IDs; the provider projects owned definitions and current
grants into the runtime snapshot. Account bearer credentials grant no management
rights. Invalid resource selections produce a rejectable runtime snapshot.

## Gateway delivery

The default `worker_mode` is `gateway`. The PHP claimer waits until its own
process's delivery adapter is mounted and durable idempotent enqueue is available.
Claims and `work.ready` outbox events are committed together. The supervisor
reserves only those events and enqueues opaque claim IDs to `weaver.work` with a
stable extension/event ID and immutable payload fingerprint. It acknowledges an
outbox reservation only after Gateway accepts the durable receipt. Reservation
tokens and process replacements do not change receipt identity.

Gateway invokes the private process-local control bridge to execute the claim.
The scheduler's atomic execution gate rejects stale or duplicate claims. Other
outbox event types remain retained for a separately configured publisher.
Capability or adapter ownership loss disarms the claimer and stops the runtime;
readiness guides admission, while database leases and attempt fencing provide
execution correctness during races. Explicit `worker_mode: poll` is for isolated
development and tests. There is no automatic polling fallback.

## Operational installation

Migration is explicit and independent of Laravel or Symfony. Activation checks
schema readiness; it never installs, adopts or rewrites existing standalone data.
An operator with a private context file can run the packaged PHP CLI with:

```sh
GATEWAY_WEAVER_CONTEXT=/private/context.json php bin/migrate --install
```

The private file has keys `configuration`, `database` and `authentication`, whose
values are the approved document and resolved scoped secrets described above.
Keep it outside the verified package. Do not commit or put it in Planner desired
state. In the minimal artifact invoke the bundled `runtime/loader`, PHP binary
and generated ini rather than assuming a host PHP installation.

Installation is idempotent for this package's schema version and preserves queue
capacity and rows. Unknown versions, partial installs and unversioned standalone
tables fail closed. Existing standalone data needs the copied-data migration and
rollback rehearsal; `schema/mysql-queue-rpc-upgrade.sql` is retained for that
explicit maintenance path, never an activation hook.

The routing reservation pins authority/definition/context; queue rows remain
current truth. Worker claims, attempts, outbox IDs, callback deduplication and
provider-effect keys retain the established engine semantics. Shutdown drains
HTTP responses within a bounded deadline before tearing down PHP, stops new
worker claims and leaves uncertain work to fenced leases and reconciliation.

## Standalone app retirement

After replacement installation, copied-data migration and rollback acceptance,
the old WorkFlow application may move under `examples/` as a test and reference
host. It should not remain a second supported production host. The independent
engine and authoring CLI remain WorkFlow-owned. No legacy source is removed by
this extraction.
