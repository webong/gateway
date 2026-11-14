# Planner on MySQL 8.4 (2026-11-14)

The standalone Planner and the PHP hosts now run on MySQL 8.4 as well as PostgreSQL.
The Router's shared delivery queue stays PostgreSQL-only, and MariaDB is not
supported at runtime. Operator steps and the support table are in
[Deployment](../deployment.md#database-support).

Proxy v0.3.1 had already removed the registry's MySQL blocker
([webong/proxy#1](https://github.com/webong/proxy/issues/1)); what was missing was
the Planner's own runtime.

## What changed

| Area | Change |
| --- | --- |
| `app/planner/config.php` | A `mysql` connection (URL, host, port, socket, utf8mb4, strict mode, `READ COMMITTED`, UTC, optional TLS CA). |
| `scripts/planner-start` | Accepts `DB_CONNECTION=pgsql` or `mysql` (still requires `DB_URL`). |
| `scripts/router-start` | Unchanged: shared deliveries still require PostgreSQL and are refused with MySQL; the spool path works with a MySQL registry. |
| Images | `pdo_mysql` added to the Planner, Router and Planner fixture images. |
| `pkg/planner/migrations/20261103000001_create_gateway_planner_schema.php` | On MySQL only: tables are `utf8mb4_0900_bin` and timestamp columns are `DATETIME`. SQLite and PostgreSQL are untouched. |

## What differed from PostgreSQL, measured

Probed on `mysql:8.4` (8.4.13; server default collation `utf8mb4_0900_ai_ci`,
isolation `REPEATABLE-READ`) against the schema the migrations created before this
change:

| Behaviour | Before | Fix |
| --- | --- | --- |
| Case in a unique key | `accounts.token_hash` `aaaa…` then `AAAA…` failed with error 1062 (PostgreSQL accepts both) | Gateway's tables are created `utf8mb4_0900_bin`. Phinx's default is `utf8mb4_unicode_ci` whatever the database default is, so the migration sets the collation itself. |
| Trailing spaces and accents | `'a' = 'a '` is true under the default collations | Same fix (`_0900_bin` is NO PAD). |
| JSON text | `{"b":1, "a":  2, "url":"https:\/\/x.test"}` came back as `{"a": 2, "b": 1, "url": "https://x.test"}` | Not changed. Nothing Gateway stores is hashed or signed from stored text (request bodies are stored base64-encoded inside the JSON); asserted by a test and documented. |
| `TIMESTAMP` range | `2040-01-01` for `deliveries.idempotency_expires_at` failed with error 1292 | The 55 timestamp columns that `gateway:migrate` creates are `DATETIME` on MySQL. Tables made by Laravel-style migrations keep `TIMESTAMP` (31 columns in a fully migrated database): Proxy's audit stamps, the Email extension's `expires_at`, `lease_until`, `next_attempt_at` and `occurred_at`, and Phinx's own history table. Documented, not changed: those values are short-lived. |
| Session time zone | Server `SYSTEM` | The connection sets `+00:00`. |
| Isolation | `REPEATABLE-READ` | The connection sets `READ COMMITTED`, PostgreSQL's default. |

Tables made by Laravel-style migrations (Proxy's `endpoints`, `endpoint_registrations`,
`profiles` and `subscriptions`, the Email extension's tables, Laravel's `migrations`
table) inherit the database default collation, so the documentation tells operators
to create the database as `utf8mb4_0900_bin`. A test creates the database with that
collation and checks that every table except Phinx's own history table is exact.

## Tests

All run when `GATEWAY_TEST_MYSQL_DATABASE` is set (CI's Planner job already has the
`mysql:8.4` service) and skip otherwise.

| Test | What it proves |
| --- | --- |
| `PlannerMysqlRuntimeTest` serves a subscription and plans a delivery | The real `app/planner/config.php` connection from `DB_URL`; `gateway:migrate` plus Proxy's migrations; a registry subscription through the HTTP API with a proxy profile; a planned and serialized relay; the connection is `READ-COMMITTED`, `+00:00` and `utf8mb4_0900_bin`; a second `gateway:migrate` is a no-op. |
| `…` compares keys exactly | In a database whose default is the case-insensitive collation: every Gateway table is `utf8mb4_0900_bin`; `Alpha`, `alpha` and `alpha ` coexist as target names; two tokens differing only by case coexist. |
| `…` stores dates after 2038 and JSON that keeps its meaning | All lease, schedule and heartbeat columns are `datetime`; a schedule at 2090-06-15 round-trips; JSON keeps its meaning and an embedded body survives byte for byte. |
| `…` takes and releases database cache locks | The `database` cache store's value, lock contention, release and takeover of an expired lock. |
| `…` gives Proxy's tables exact comparisons | See above. |
| `WorkermanPlannerTest` serves planning and management requests from a Workerman host on MySQL | A real Workerman process on MySQL: health, authenticated planning, pass-through, management create and list, exact names, rows in the disposable database. |
| `…` leaves a sentinel MySQL database untouched when `DB_URL` points at it | Isolation: a positive control shows `DB_URL` would redirect the host; the disposable run still uses its own database; the sentinel's tables and row counts are unchanged. The resolved-database check now covers MySQL (database, endpoint and server UUID). |
| `…` reconnects to MySQL on the same worker after its connection is killed | `KILL CONNECTION` on the worker's own thread; the first request afterwards succeeds on a new thread in the same worker. |
| `…` rolls back a write left in an open transaction on MySQL | A request that writes and ends without committing: the worker's next request sees transaction level 0, the row is absent, and `information_schema.innodb_trx` has no transaction for the worker's thread. |
| `scripts/test-launcher-databases` | `planner-start` accepts `pgsql` and `mysql` only and needs `DB_URL`; `router-start` refuses shared deliveries on MySQL; `router-start` migrates and boots on a real MySQL registry with the spool path, twice. |

### Negative controls

Each was broken once and the matching test failed, then the file was restored:

- Migration without the collation and `DATETIME` change: the exact-comparison, 2038 and
  Proxy-collation tests fail.
- Connection without isolation, time zone and collation: the runtime test fails.
- Handler without its per-request rollback: the rollback test fails (`transaction_level` 1).
- `DB_URL` no longer neutralised for the host: the sentinel test refuses to write.
- `planner-start` accepting only `pgsql`: the launcher test fails.

## Runs

Disposable `mysql:8.4` (8.4.13) and `postgres:17` (17.11) containers, macOS arm64,
PHP 8.5.8, Go 1.27.2.

| Check | Command | Result |
| --- | --- | --- |
| Full PHP suite | `vendor/bin/pest --compact` with `GATEWAY_TEST_POSTGRES_*` and `GATEWAY_TEST_MYSQL_*` | 500 passed, 2 skipped (4,737 assertions), 0 failed. The two skips are the PostgreSQL verified-TLS tests (`GATEWAY_TEST_POSTGRES_SSLROOTCERT` unset). The suite had 493 tests in CI before this change; 9 are new. A first run failed 3 `ExtensionArtifactReconciliationTest` cases because the fresh worktree lacked the HookScript companion's dependencies, which CI installs in its own step; with them installed that file passes |
| MySQL runtime tests | `vendor/bin/pest tests/Feature/PlannerMysqlRuntimeTest.php` | 5 passed (46 assertions) |
| MySQL Workerman tests | `vendor/bin/pest tests/Feature/WorkermanPlannerTest.php --filter MySQL` | 4 passed (69 assertions) |
| Launchers | `GATEWAY_TEST_MYSQL_DATABASE=… bash scripts/test-launcher-databases` | passes, including two boots of `router-start` on MySQL |
| Planner image | `docker build -f infra/docker/Gateway.planner.Dockerfile .` | builds; `php -m` lists `pdo_mysql`, and `pdo_mysql.so` is in the FrankenPHP extension set |

**The image against MySQL.** A container from that image with `DB_CONNECTION=mysql` and a
`DB_URL` for a database created as `utf8mb4_0900_bin`, on the default FrankenPHP host:

- `serve` applied Gateway's migration, Proxy's five and the application's; `/health` answered 204.
- `/_internal/gateway/ready` answered 204 with the internal token and 404 without it.
- `POST /targets` created `image-proof-target` and then `Image-Proof-Target` (both 201);
  `GET /targets` returned both; planning an unbound path returned `pass_through`.
- In MySQL: 34 tables `utf8mb4_0900_bin` (the exception is Phinx's history table),
  55 `datetime` and 31 `timestamp` columns (the latter from Laravel-style migrations).
- `docker stop` returned in 1 s.

Not run here: the Router images (CI builds both), the Octane, FPM and Workerman hosts
inside the image on MySQL (Workerman has its own MySQL tests above; the others use the
same Laravel connection), and the `docker-smoke` and Kubernetes scripts.

## Not covered

- **MariaDB** at runtime, and any MySQL older than 8.0 (`utf8mb4_0900_bin` is MySQL 8.0+).
- **Shared deliveries on MySQL** (the Go queue and delivery-admin discovery).
- **Load, latency and concurrency** on MySQL. The `READ COMMITTED` setting is a
  choice for parity with PostgreSQL; no contention test was run.
- **A MySQL TLS connection** (`MYSQL_ATTR_SSL_CA` is wired in, not exercised).
- **Symfony hosts on MySQL** beyond the migrations the existing tests cover.

## A test-harness hazard found on the way

A request that makes the application throw left the Workerman test host unable to stop
on SIGQUIT. It is not a production defect: the harness started the host with the
`APP_ENV=testing` it inherits from Pest, and under that value Laravel keeps every logged
exception, and through its stack-trace arguments the request's connection, for the life of
the process. [PR #27](https://github.com/webong/gateway/pull/27) starts the host as
production and adds a regression test; see
[Workerman stop after a request that throws](planner-workerman-exception-stop-2026-11-14.md)
once it merges. The MySQL rollback test here ends a request with an open transaction
instead of one that throws, which avoids the hazard and exercises the same cleanup.
