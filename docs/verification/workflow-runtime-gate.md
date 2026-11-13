# WorkFlow runtime gate — initial evidence

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

## Gates not yet satisfied

- The architecture proof passes, but it is an independent Ext consumer harness rather than the production host/deployment matrix.
- This is a disposable SQLite workload. PDO driver presence is not a PostgreSQL/MySQL integration claim; the prerequisite WorkFlow SQL matrix remains separate evidence.
- Standalone Spinner and both Router host acceptance, live capability-loss handling and real deployment replacement are not verified by the independent Ext consumer harness.
- Production definition/grant resources, credentials, provider effects, durable delivery binding and external released-core consumption are not implemented here.
- WorkFlow's only release, `v0.1.0-rc.1`, predates required queue contracts. Gate 3 needs a compatible independent core release before extraction can satisfy external-consumer acceptance.
- Copied-data migration/rollback, artifact release, installation and deprecation are not done. No old app source was removed, no production data was migrated, and no artifacts were published.

ADR 0012 remains proposed. Gate 2 stays open until its complete architecture/runtime and host requirements have evidence; later gates must not be checked off from this prototype.
