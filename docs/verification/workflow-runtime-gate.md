# WorkFlow runtime and host evidence

Issue: https://github.com/webong/gateway/issues/18
Gateway base: `c0879dc48192af8ec47213647bae242c033b784a`.
Clean WorkFlow source: `00e0bbcdd61c2c6ed96af55865d0ef2e77557adb`.
Prerequisite post-merge CI: https://github.com/webong/workflow/actions/runs/43508334306 (passed).

## Verified locally

Linux arm64, Docker/OrbStack on an arm64 host. Source image `sha256:531d6d3d30083f28f0584b99f4a46019fc5b9cfaee73315f81fd9452c249c8a9`; scratch runtime probe image `sha256:bb7b329d692b5004151f12adebad67608ac5a54c3f1aaf7daff6ec4767dd216e`.
PHP reported 8.3.35. Activation requires the bundled runtime to report PDO SQLite/PostgreSQL/MySQL and pcntl. The scratch image has no system PHP, shell, ELF loader, libraries or toolchain outside the packaged runtime closure.

Commands:

```sh
GOWORK=off go -C tools/workflow-runtime-gate test -race ./...
GOWORK=off go -C tools/workflow-runtime-gate vet ./...
python3 tools/workflow-runtime-gate/smoke.py gateway-workflow-runtime-gate:arm64
```

Result:

```text
PASS: authenticated queue start, scoped IDs, deferred callback/deduplication, dependent completion, cancellation
PASS: pinned queue state survived runtime replacement
PASS: reserved an unexecuted claim before process crash
PASS: expired claim recovered after crash; stale claim fenced; dependent completion preserved
PASS: reserved an unexecuted claim before process crash
PASS: uncertain non-idempotent attempt requires reconciliation; no automatic replay
PASS: missing scoped secret fails closed
PASS: changed/missing runtime rejected before activation
PASS: scratch runtime/PDO readiness, private advertisement and bounded clean shutdown
```

The fixture pauses its worker **between** transactions before reserving an abandoned claim; freezing it while holding SQLite's write lock tests an artificial lock, not lease recovery. Recovery is asserted separately for the explicit idempotent policy and the non-idempotent policy. The latter correctly records `claim_lost` and does not automatically replay the attempt. No exactly-once external-effect guarantee is asserted.

The Ext consumer harness re-verifies all manifest artifacts, launches the real JSON-line guest, and binds only the declared secret operation to the fixture identity. The guest advertises only a private `/workflow/rpc` mount after PHP/PDO, queue worker and RPC bootstrap checks. Missing secret or altered PHP content produces no mount. Publisher trust/approval is a separate host responsibility; local checksums alone do not establish it.

Go race tests exercise terminal-child failure stopping sibling processes, rejecting new work after closure, missing bundled runtime, and rejecting client-selected executable/bootstrap settings. `actionlint:1.7.7` passes the new native runtime workflow.

## Native architecture CI

The same probe code at `d1663992d0a89741071023bdb9a7aa4356a9c57d` passed on both native GitHub runners:

- [Linux amd64](https://github.com/webong/gateway/actions/runs/43512343763/job/132185475648): supervision/race tests, independent contract imports, pinned source/runtime closure build and the complete Ext/RPC/recovery smoke passed.
- [Linux arm64](https://github.com/webong/gateway/actions/runs/43512343763/job/132185474786): the same checks passed.

Draft implementation PR: https://github.com/webong/gateway/pull/24 . These are native runner results, not a cross-compile-only or emulated-runtime claim. No images or archives were published.

## Actual production host matrix

Implementation: `9fed9b07da838c0143d5ba136a2fd151a5815b74`, including Gateway
main `534239279ba4a21229e34ce580312d100f2e3146`.
[Native host-matrix run](https://github.com/webong/gateway/actions/runs/43525384629):
[amd64](https://github.com/webong/gateway/actions/runs/43525384629/job/132229729468),
[arm64](https://github.com/webong/gateway/actions/runs/43525384629/job/132229729897).
The linked run records the result for each architecture; source changes after this
implementation commit must rerun the matrix before relying on that evidence.

`host-build.sh` builds the production Spinner, RoadRunner Router and FrankenPHP
Router Dockerfiles. `host-matrix.py` installs the package exported from the same
architecture-specific runtime image into each host's verified artifact cache.
Standalone Spinner uses a separate production Planner application; both Routers
use their actual production entrypoints and privilege drop. Setup uses production
activation/secret stores through trusted disposable CLI fixtures, not testbench
providers or a public setup route.

Each host exercises independent host/Extension credentials, private-only routing,
authenticated queue start, subject-scoped identities, deferred callback replay,
dependent completion, cancellation, host restart, expired-claim recovery, stale
claim fencing and non-idempotent crash reconciliation. It also kills the guest
without killing the host, then asserts its PHP children stop; disable/reactivate
and scoped-secret loss must close the runtime and stop claims. Local arm64
Spinner and RoadRunner acceptance passed; the native matrix is the authoritative
cross-architecture record.

The shared host fixes are regression-tested: private fallback strips host and
internal credentials before forwarding the separate Extension credential;
optional `gateway.runtime/v1.stop` runs with a ten-second bound before closing
Ext. Existing packages without `stop` keep their prior close behavior. The
prototype additionally uses Linux parent-death signalling for abrupt termination
and monitors its scoped secret. These changes do not implement a production
WorkFlow configuration/resource model.

## Remaining boundaries

- The installed-host fixture uses disposable SQLite state and seeded verified
  caches. It does not test fresh HTTPS artifact acquisition or publisher trust.
- PDO presence is not PostgreSQL/MySQL application integration. The extracted
  PHP 8.3 package must pass SQLite, PostgreSQL and MySQL queue/RPC acceptance with
  real services and no service-related skips.
- Weaver application/runtime extraction now lives in `pkg/weaver`; Planner definition/grant resources, provider effects and durable-delivery binding remain open.
- The published WorkFlow `v0.1.0-rc.1` predates required queue contracts. Gate 3
  requires a compatible independent core release and clean external consumption.
- Copied-data migration/rollback, artifact publication, fresh installation and
  deprecation remain open. No old app source was removed or production data moved.

See [the complete remaining-work checklist](workflow-migration-remaining.md) for
gates 3–7 and the runtime ADR review. ADR 0012 remains proposed until reviewed with
the host evidence; later gates cannot be checked off from the runtime proof.

## Weaver extraction (2026-11-14)

The user selected **Weaver**, identity `webong/gateway-weaver`, namespace
`Webong\Gateway\Weaver`, under `pkg/weaver`. The independent engine remains
`webong/workflow`. Private `/workflow/rpc` and public logical run IDs are retained.

The actual package now owns its bridge, supervised worker, PDO stores, queue/RPC
adapter and explicit schema installer. Its development build consumes the pinned
external core through Composer, not copied source or a path dependency. Local
PHP 8.3 SQLite/PostgreSQL/MySQL acceptance passed 126 tests / 691 assertions with
no skips including the startup-credential regression; the expanded
native workflow records the final source results.

New runtime policy includes transport-derived account principals, exact
account/flow/subject/action grants, account-scoped storage identities, approved
operation selection and versioned definitions. Credentials are resolved separately
and validated before any worker claims. Installation never adopts legacy data
or changes authority automatically. Slow HTTP acknowledgements drain before PHP
teardown, within the supervisor's bounded shutdown deadline.

The actual development artifact has passed local Spinner, RoadRunner and
FrankenPHP host acceptance. CI now
builds Weaver and tests its SQL, real bridge/worker and all three host cases on
both native architectures. Earlier linked runtime jobs are historical prototype
evidence, not evidence for new uncommitted Weaver source.

**PR #24 must remain unmerged until the full extension setup is complete.**
Compatible released-core consumption, the actual Planner management entrypoint,
durable-delivery binding, copied-data migration/rollback and release/install
acceptance remain open; see `pkg/weaver/README.md` and the remaining-work list.
