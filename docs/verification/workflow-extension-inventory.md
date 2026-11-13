# WorkFlow extension inventory — issue #18

Source: `webong/workflow` at `00e0bbcdd61c2c6ed96af55865d0ef2e77557adb` (merged queue RPC prerequisite).
Gateway baseline: `c0879dc48192af8ec47213647bae242c033b784a`.
Post-merge prerequisite CI: https://github.com/webong/workflow/actions/runs/43508334306 (success).
The source checkout is clean and pinned; unrelated Composer projection work is excluded.

Every tracked `app/` file is accounted for below. Short hashes identify the inspected source, not release checksums.
No file has been removed or moved from WorkFlow. This inventory assigns eventual ownership; it does not claim extraction is complete.

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

`src/`, `ext/Laravel`, `ext/Temporal`, `ext/OpenWorkflow`, native compiler builds and the independent Composer package remain in WorkFlow. The migration must not make those consumers depend on Gateway. Existing `Webong\WorkFlow\App` consumers remain supported until a replacement is separately released; moved production classes will use `Webong\Gateway\Workflow`.

The SQL files under `app/schema` create RPC routing reservations; QueueScheduler::install creates queue runs/claims/outbox tables separately. Preserve both, plus scoped RPC identity and pinned authority. `mysql-queue-rpc-upgrade.sql` is an explicit maintenance operation, never an activation hook.

## Gate status

- Prerequisite: merged and post-merge CI verified.
- Inventory: completed against the exact source above.
- Runtime decision: ADR 0012 proposes a bundled Linux runtime, pending feasibility evidence.
- Runtime proof: in progress under `tools/workflow-runtime-gate`; not a production extension.
- Extraction, Planner resources/permissions/secrets, host/delivery matrix, copied-data migration/rollback and release/deprecation: not started.

Do not advance extraction until architecture-specific runtime/PDO, private routing, worker readiness, shutdown and lease-recovery evidence exists. A local image build alone is insufficient. The queue remains authoritative; no automatic authority conversion is planned.

## Released-core prerequisite

At inventory time the only GitHub release is WorkFlow `v0.1.0-rc.1`. Comparing it to the merged prerequisite shows changes to `FlowQueueScheduler`, `FlowStepClaim`, `FlowDeferredCompletion`, `FlowReadySet`, `FlowStateTransition` and related execution types. A production `webong/gateway-workflow` dependency cannot honestly target that older release while requiring the new contracts. Gate 3 therefore also requires a compatible separately published WorkFlow core release; the feasibility probe may consume the exact merged source, but that is not external released-package acceptance.
