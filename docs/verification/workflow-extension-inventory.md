# WorkFlow extension inventory — issue #18

Source: `webong/workflow` at `00e0bbcdd61c2c6ed96af55865d0ef2e77557adb` (merged queue RPC prerequisite).
Gateway baseline: `c0879dc48192af8ec47213647bae242c033b784a`.
Post-merge prerequisite CI: https://github.com/webong/workflow/actions/runs/43508334306 (success).
The source checkout is clean and pinned; unrelated Composer projection work is excluded.

Every tracked `app/` file is accounted for below. Short hashes identify the inspected source, not release checksums.
Application runtime/store code, bridge and regression tests have been copied into `pkg/weaver` under `Webong\Gateway\Weaver`. No old WorkFlow source was removed. The runtime now consumes released core v0.1.0-rc.2; the full extension setup remains gated. The user selected the name **Weaver** on 2026-11-14; it uses `webong/gateway-weaver` while the engine remains `webong/workflow`.

| Source file | Intended owner / treatment | SHA-256 prefix |
| --- | --- | --- |
| `app/Dockerfile` | Build/acceptance tooling; retain standalone compatibility | `bf43fa027daa` |
| `app/README.md` | Build/runtime documentation and dependency metadata; retain standalone compatibility | `76d7e1865a73` |
| `app/bootstrap.example.php` | Trusted example configuration; replace environment secrets with scoped capability before release | `817f36e70ec7` |
| `app/cmd/workflowd/main.go` | Runtime bridge implementation; adapt entrypoint to Arranger lifecycle | `216e5d6519b1` |
| `app/compose.ci.yml` | Build/acceptance tooling; retain standalone compatibility | `840beefcb6e6` |
| `app/compose.yml` | Build/acceptance tooling; retain standalone compatibility | `28e5b93b3aaf` |
| `app/go.mod` | Build/runtime documentation and dependency metadata; retain standalone compatibility | `1a4118458a61` |
| `app/go.sum` | Build/runtime documentation and dependency metadata; retain standalone compatibility | `7764dcff041c` |
| `app/internal/rpc/handler.go` | Extension private HTTP facade and protocol regression tests | `03b0a3e6eda5` |
| `app/internal/rpc/handler_test.go` | Extension private HTTP facade and protocol regression tests | `bcfd62c4db73` |
| `app/public/index.php` | Private embedded-PHP bridge; never serve PHP source publicly | `755d5ee58226` |
| `app/queue-worker.php` | Extension-supervised queue worker | `6eff4f65d4bc` |
| `app/runtime.example.php` | Trusted example configuration; replace environment secrets with scoped capability before release | `ca533115c64d` |
| `app/schema/mysql-queue-rpc-upgrade.sql` | Independent operational migrations; MySQL upgrade remains explicit maintenance | `682e09562cbc` |
| `app/schema/mysql.sql` | Independent operational migrations; MySQL upgrade remains explicit maintenance | `790f00823a9d` |
| `app/schema/postgres.sql` | Independent operational migrations; MySQL upgrade remains explicit maintenance | `181e432fb8c7` |
| `app/schema/sqlite.sql` | Independent operational migrations; MySQL upgrade remains explicit maintenance | `cf3360cc2123` |
| `app/src/FlowAuthoringCli.php` | Trusted authoring tooling; keep legacy CLI compatibility | `67c7d107ffaa` |
| `app/src/FlowRpcApplication.php` | Extension runtime (Gateway-owned namespace) | `946b9124d739` |
| `app/src/PdoFlowStateStore.php` | Extension runtime (Gateway-owned namespace) | `183288ebc8ed` |
| `app/src/PdoMySqlFlowStateStore.php` | Extension runtime (Gateway-owned namespace) | `51f84d537044` |
| `app/src/PdoMySqlFlowStateStoreFactory.php` | Extension runtime (Gateway-owned namespace) | `7f26e31ea75f` |
| `app/src/PdoPostgresFlowStateStore.php` | Extension runtime (Gateway-owned namespace) | `d6e4a599affb` |
| `app/src/PdoPostgresFlowStateStoreFactory.php` | Extension runtime (Gateway-owned namespace) | `15bb639f17c8` |
| `app/src/PdoSqliteFlowStateStore.php` | Extension runtime (Gateway-owned namespace) | `61286a1ea926` |
| `app/src/PdoSqliteFlowStateStoreFactory.php` | Extension runtime (Gateway-owned namespace) | `07f4214f731d` |
| `app/src/QueueOutboxDispatcher.php` | Extension runtime (Gateway-owned namespace) | `a6c82fa59c98` |
| `app/src/QueueRpcRuntime.php` | Extension runtime (Gateway-owned namespace) | `d32e47225b9a` |
| `app/src/QueueScheduler.php` | Extension runtime (Gateway-owned namespace) | `5d03aabcfb5c` |
| `app/src/QueueWorker.php` | Extension runtime (Gateway-owned namespace) | `64835e62e103` |
| `app/src/RpcFailure.php` | Extension runtime (Gateway-owned namespace) | `49518612e0ae` |
| `app/src/RpcRunIdentity.php` | Extension runtime (Gateway-owned namespace) | `3fdeadb9e831` |
| `app/tests/smoke.py` | Build/acceptance tooling; retain standalone compatibility | `dafac73c4888` |

## Boundaries retained

`src/`, `ext/Laravel`, `ext/Temporal`, `ext/OpenWorkflow`, native compiler builds and the independent Composer package remain in WorkFlow. The migration must not make those consumers depend on Gateway. Existing `Webong\WorkFlow\App` consumers remain supported until a replacement is separately released; moved production classes will use `Webong\Gateway\Weaver`.

The SQL files under `app/schema` create RPC routing reservations; QueueScheduler::install creates queue runs/claims/outbox tables separately. Preserve both, plus scoped RPC identity and pinned authority. `mysql-queue-rpc-upgrade.sql` is an explicit maintenance operation, never an activation hook.

## Gate status

- Prerequisite: merged and post-merge CI verified.
- Inventory: completed against the exact source above; legacy authoring CLI remains core-owned.
- Runtime proof: native amd64/arm64 scratch and all three production host checks passed in [PR #24](https://github.com/webong/gateway/pull/24).
- Weaver extraction: application/runtime stores and Go/PHP bridge now live in `pkg/weaver`; the core is an external Composer dependency pinned to released v0.1.0-rc.2 with consumer-root core projection. SQL/HTTP/host acceptance is being rerun against this artifact.
- Planner provider/resources/grant projection and durable Gateway delivery are implemented, with installed-host acceptance in progress. Copied-data cutover/rollback and release/deprecation remain open. The old app is intended to become an example/test host after replacement acceptance.
- Keep PR #24 unmerged until the full extension setup is complete, as requested by the user.

## Released-core prerequisite

At inventory time the only GitHub release is WorkFlow `v0.1.0-rc.1`. Comparing it to the merged prerequisite shows changes to `FlowQueueScheduler`, `FlowStepClaim`, `FlowDeferredCompletion`, `FlowReadySet`, `FlowStateTransition` and related execution types. A production `webong/gateway-weaver` dependency cannot honestly target that older release while requiring the new contracts. Gate 3 therefore also requires a compatible separately published WorkFlow core release; the feasibility probe may consume the exact merged source, but that is not external released-package acceptance.

WorkFlow `v0.1.0-rc.2` was subsequently released at `278861082e00537fe7e759eb7c8bee3e7a3f0bf4`. Weaver now installs its tagged dist with the root-owned source projection plugin. The original source feasibility probe remains pinned to its inventoried commit.
