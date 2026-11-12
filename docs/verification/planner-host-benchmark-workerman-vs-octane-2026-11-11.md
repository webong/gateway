# Planner host benchmark: Workerman vs Laravel Octane (FrankenPHP)

Measured 2026-11-11 and 2026-11-12. This documents one workload on one machine, over several passes; it makes no claim beyond that.

## What was compared

| | Workerman | Octane |
|---|---|---|
| Runtime | Workerman v5.2.3 | Laravel Octane v2.20.0 on FrankenPHP v1.13.5 PHP 8.4.27 |
| Launch path | `gateway-planner-start serve`, `GATEWAY_PLANNER_SERVER=workerman` | `gateway-planner-start serve`, `GATEWAY_PLANNER_SERVER=frankenphp` (the image default) |
| PHP | 8.4.27 | 8.4.27 |

The one Octane server tested is **FrankenPHP**. Swoole and RoadRunner also start and serve from the same image (a short smoke run, not measured here).
Both hosts run from the same image, built from this checkout, with the same worker count (4), recycling threshold (500 requests per worker), CPU and memory limit (`--cpus 4 --memory 2g`), environment, and PHP settings (one shared ini, below). Each serves its own PostgreSQL database cloned from one migrated and seeded template, on one PostgreSQL server. Before any database write the harness verified that Laravel resolves the intended database on the intended PostgreSQL server (database name, endpoint, server identity, no config cache) for the template, every clone, and every running host.

Passes A and B also include `workerman-shipped`, a reference row: Workerman with the image's own PHP settings, where the CLI PHP has `opcache.enable_cli=0`. It is not an equal-settings comparison.

## Workload

Authenticated `POST /_internal/gateway/plan` to `/http-smoke/registry-ingress`, resolving to one seeded endpoint and relay subscription (database-backed, cache store `database`). Every measured response must be HTTP 200 and the seeded relay plan (action, relay URL, subscriber, endpoint key). Timeouts, transport errors, HTTP errors and wrong plans count as failures; none are retried. Clients keep connections alive. Warm-up: 1500 requests at concurrency 8 plus 300 at concurrency 1 per host, and 200 unmeasured requests before every measured run. Measured: 5000 requests per run at concurrency 1 and 8, host order alternated each repetition.

## Reproduce

```bash
# from the repository root, with Docker running and PHP (curl, pcntl) on the client
php scripts/bench-planner-hosts.php --build-only      # builds gateway-planner-bench:local from this tree
php scripts/bench-planner-hosts.php --hosts=workerman,frankenphp --repetitions=7
```

The harness refuses to run if the image was built from a different tree than the working tree (`--allow-stale-image` overrides; the pass that used it is marked below). Every `docker` command it ran is recorded in `commands.txt` in its output directory; the ones for the headline pass are at the end of this document. Defaults: `--workers=4 --max-requests=500 --cpus=4 --memory=2g --concurrency=1,8 --requests=5000 --warmup=1500 --prewarm=200 --sample-interval=0.5`.

## Headline pass (pass C)

### Median across 7 runs

| Host | Runtime | Workers | Conc. | Requests/run | Errors (total) | req/s | p50 ms | p95 ms | p99 ms | Peak serving RSS MiB |
|---|---|---|---|---|---|---|---|---|---|---|
| workerman | Workerman v5.2.3, PHP 8.4.27 | 4 | 1 | 5000 | 0 | 140 | 5.90 | 14.40 | 22.58 | 181.4 |
| frankenphp | Laravel Octane v2.20.0 on FrankenPHP v1.13.5 PHP 8.4.27 Caddy v2.11.9 h1:bhZtcRKQOq0fuLvEgCdPYvUxXyGn9kzjG4lh2332DTE=, PHP 8.4.27 | 4 | 1 | 5000 | 0 | 120 | 7.03 | 16.52 | 26.74 | 94.3 |
| workerman | Workerman v5.2.3, PHP 8.4.27 | 4 | 8 | 5000 | 4 | 545 | 11.51 | 32.52 | 58.87 | 192.3 |
| frankenphp | Laravel Octane v2.20.0 on FrankenPHP v1.13.5 PHP 8.4.27 Caddy v2.11.9 h1:bhZtcRKQOq0fuLvEgCdPYvUxXyGn9kzjG4lh2332DTE=, PHP 8.4.27 | 4 | 8 | 5000 | 0 | 750 | 9.11 | 20.93 | 31.82 | 94.6 |

### Memory methodology

- A separate process runs `docker exec <container>` every 0.5 s and reads `VmRSS` of every process in the container; it runs outside the timed client loop.
- "Serving" RSS is the sum over the processes that handle requests: Workerman's worker processes (the master is reported separately), or FrankenPHP's single process (its worker threads share one address space, so this is one number for all threads, not per worker).
- "Peak" is the maximum of those sums over the samples taken during the timed run, so it is a sampled peak (it can miss a spike shorter than the interval). Replacement workers created by recycling are included because the sum is taken over whatever is running at each sample.

### Runtime settings actually read (each runtime's own php)

- **workerman**: {"memory_limit":"128M","opcache.enable":"1","opcache.enable_cli":"1","opcache.memory_consumption":"128","opcache.validate_timestamps":"1","opcache.jit":"disable","zend.assertions":"-1"}
- **frankenphp**: {"memory_limit":"128M","opcache.enable":"1","opcache.enable_cli":"1","opcache.memory_consumption":"128","opcache.validate_timestamps":"1","opcache.jit":"disable","zend.assertions":"-1"}

### Environment

- Tree tested: `533f7facbac376afd781a9c1a0ea072408f86611` (fix: restore Planner CI and migrate bundled Router schema); working-tree fingerprint `dcbc4a5846224bf1010474b8cd2f8fa4ee87bc62`; image `sha256:12fc45cfbfd9a25f1718cee251c667e47e3f69a8da97ea347a14c9aea02f19f9` built from tree `0d97b14f32c2662c8c2fcb282b434617968b0c75`.
- Uncommitted changes in the tested tree (excluding the harness): 
```
M .github/workflows/ci.yml
 M Makefile
 M app/WorkermanLaravelHandler.php
 M app/workerman.php
 M infra/docker/Gateway.planner.Dockerfile
 M pkg/planner/README.md
 M pkg/planner/src/Laravel/Console/LaravelGatewayMigrateCommand.php
 M scripts/bench-planner-workerman.php
 M scripts/planner-start
 M tests/Feature/GatewayMigrationCommandTest.php
 M tests/Feature/WorkermanPlannerTest.php
 M tests/Support/WorkermanPlannerHelper.php
 M tests/Support/seed-planner-endpoint.php
 M tests/Unit/WorkermanPersistentWorkerTest.php
?? docs/verification/planner-host-benchmark-workerman-vs-octane-2026-11-11.md
?? docs/verification/planner-workerman-graceful-shutdown-2026-11-12.md
?? scripts/test-planner-workerman-shutdown
?? tests/Fixtures/InstrumentedHttpSmokePathResolver.php
?? tests/Support/resolve-planner-database.php
```
- Load average on the client machine (1/5/15 min): at start 8.49 8.02 6.56, at end 9.39 9.83 8.72. Other applications were running on this machine during the measurement.
- Client: macOS host `Darwin 23.6.0 arm64`, Apple M3 Pro (11 cores), PHP 8.5.8 (Darwin), curl 8.21.0, outside the containers.
- Containers: Docker 29.8.2 (OrbStack, aarch64), VM with 11 CPUs and 11.7 GiB; each host limited to --cpus 4, --memory 2g; PostgreSQL 18.2 (Debian 18.2-1.pgdg13+1) in a container on the same network (default settings except max_connections=300).
- Workload: authenticated POST /_internal/gateway/plan, /http-smoke/registry-ingress, one seeded endpoint and relay subscription; database-backed (PostgreSQL), cache store `database` (the image default for both hosts).
- Warm-up: 1500 requests at concurrency 8 plus 300 at 1 per host after start, and 200 unmeasured requests before every measured run. Recycling after 500 requests per worker applies to every host during the measured runs.
- Measured load: 5000 requests per run at each concurrency, 7 repetitions, host order alternated per repetition, client connections kept alive, per-request timeout 10 s. Failures (timeout, transport error, non-200, wrong plan) are counted; none are retried.

## How much the passes differ

Median throughput in req/s (min–max over runs; n = number of runs), by pass:

| Host | Conc. | Pass A | Pass B | Pass C |
|---|---|---|---|---|
| workerman | 1 | 331 (324–445; n=3) | 221 (216–258; n=3) | 140 (101–302; n=7) |
| frankenphp | 1 | 248 (231–257; n=3) | 298 (205–306; n=3) | 120 (94–283; n=7) |
| workerman | 8 | 1023 (929–1257; n=3) | 754 (537–893; n=3) | 545 (348–1246; n=7) |
| frankenphp | 8 | 1098 (916–1293; n=3) | 924 (918–1262; n=3) | 750 (430–1141; n=7) |

Client-machine load average (1/5/15 min, start → end of the pass): pass C: 8.5 8.0 6.6 → 9.4 9.8 8.7. 

## What the numbers support

- **Concurrency 1:** the order of the medians changes between passes (pass A: Workerman, pass B: FrankenPHP, pass C: Workerman), so this workload does not show a stable throughput difference at this concurrency.
- **Concurrency 8:** the median throughput was higher for FrankenPHP in every pass (pass A: FrankenPHP, pass B: FrankenPHP, pass C: FrankenPHP); see the ranges above for how far the runs overlap.
  Pooled over every pass at concurrency 1 (Workerman n=13, FrankenPHP n=13): median 221 against 205 req/s; the runs overlap (Workerman 101–445, FrankenPHP 94–306).
  Pooled over every pass at concurrency 8 (Workerman n=13, FrankenPHP n=13): median 754 against 918 req/s; the runs overlap (Workerman 348–1257, FrankenPHP 430–1293).
- **Memory, workerman:** sampled peak serving RSS ranged 169–194 MiB over 26 runs.
- **Memory, frankenphp:** sampled peak serving RSS ranged 93–145 MiB over 26 runs.
  Workerman's lowest sampled peak was above FrankenPHP's highest in every run. These are different quantities (four worker processes against one process whose four worker threads share an address space), so read them as totals for the serving processes, not per worker.
- **Errors, pass A:** workerman: 0, frankenphp: 0, workerman-shipped: 2 failed requests out of 90000 measured.
- **Errors, pass B:** workerman: 1, frankenphp: 0, workerman-shipped: 0 failed requests out of 90000 measured.
- **Errors, pass C:** workerman: 4, frankenphp: 0 failed requests out of 140000 measured.
- **What the failed requests were** (samples kept by the harness, up to 5 per run): workerman-shipped: "transport: Server returned nothing (no headers, no data)" x2; workerman: "transport: Server returned nothing (no headers, no data)" x5. An empty reply on a kept-alive connection is what a client sees when the server closes the connection between its requests; Workerman closes a recycled worker's connections when it recycles (after 500 requests per worker here), whereas an Octane server recycles a worker without dropping client connections. That explanation fits the pattern but was not isolated by a separate experiment.
- **Run-to-run spread across all passes** (same configuration, same image): workerman at concurrency 1: 101–445 req/s; workerman at concurrency 8: 348–1257 req/s; frankenphp at concurrency 1: 94–306 req/s; frankenphp at concurrency 8: 430–1293 req/s. Differences smaller than this spread should not be read as a difference between hosts.

## Limitations

- One machine (Apple M3 Pro, macOS, Docker via OrbStack in an 11-CPU, 11.7 GiB VM), shared with other applications that were running throughout (the load averages above). The load client and the memory sampler run on the same machine as the containers and compete with them for CPU. Passes A and B were taken in the same session as image builds and other work.
- Between passes the same configuration varied by tens of percent, and single runs of an unchanged configuration varied several-fold (see the per-run tables). Medians of three to seven runs are a rough guide, not a statistical result.
- "4 workers" means four processes for Workerman and four threads for FrankenPHP.
- Memory is sampled every 0.5 s by `docker exec`, so a spike shorter than that can be missed.
- One endpoint, one small payload, PostgreSQL in a container on the same network, plain HTTP, ARM64. Nothing here covers TLS, HTTP/2, other routes, longer runs, or production hardware.
- FrankenPHP could not boot from the Planner image until `GATEWAY_COMPANION_PHP_BINARY` was added to `infra/docker/Gateway.planner.Dockerfile` (the Router image already sets it); that change is part of the tested tree.
- Pass A ran before, and passes B and C after, the `app/workerman.php` change that closes connections when a worker stops (see `planner-workerman-graceful-shutdown-2026-11-12.md`). It does not change how recycling closes connections.
- The headline pass used `--allow-stale-image`: its image was built from tree `0d97b14f32c2662c8c2fcb282b434617968b0c75` and the working tree then differed only by documentation added afterwards (fingerprint `dcbc4a5846224bf1010474b8cd2f8fa4ee87bc62`).

## Appendix: every pass

### Pass A

Configuration: hosts workerman, frankenphp, workerman-shipped; 3 repetitions; image `sha256:715f860cb748178ab0bdb2215cd75930c9d308f4455f4563274c622d3c11b321` built from tree `d23c92e8ff9ba048a45bf472fbeef204105cdc56`; working tree at measurement `d23c92e8ff9ba048a45bf472fbeef204105cdc56`.

### Median across 3 runs

| Host | Runtime | Workers | Conc. | Requests/run | Errors (total) | req/s | p50 ms | p95 ms | p99 ms | Peak serving RSS MiB |
|---|---|---|---|---|---|---|---|---|---|---|
| workerman | Workerman v5.2.3, PHP 8.4.27 | 4 | 1 | 5000 | 0 | 331 | 2.62 | 5.26 | 8.50 | 182.0 |
| frankenphp | Laravel Octane v2.20.0 on FrankenPHP v1.13.5 PHP 8.4.27 Caddy v2.11.9 h1:bhZtcRKQOq0fuLvEgCdPYvUxXyGn9kzjG4lh2332DTE=, PHP 8.4.27 | 4 | 1 | 5000 | 0 | 248 | 3.47 | 7.82 | 12.25 | 124.5 |
| workerman-shipped | Workerman v5.2.3, PHP 8.4.27 | 4 | 1 | 5000 | 0 | 286 | 2.95 | 6.98 | 10.05 | 175.2 |
| workerman | Workerman v5.2.3, PHP 8.4.27 | 4 | 8 | 5000 | 0 | 1023 | 6.41 | 16.35 | 24.32 | 192.0 |
| frankenphp | Laravel Octane v2.20.0 on FrankenPHP v1.13.5 PHP 8.4.27 Caddy v2.11.9 h1:bhZtcRKQOq0fuLvEgCdPYvUxXyGn9kzjG4lh2332DTE=, PHP 8.4.27 | 4 | 8 | 5000 | 0 | 1098 | 5.77 | 15.46 | 28.92 | 94.6 |
| workerman-shipped | Workerman v5.2.3, PHP 8.4.27 | 4 | 8 | 5000 | 2 | 920 | 6.85 | 19.63 | 27.87 | 186.8 |

### Per-run results

| Host | Workers | Conc. | Rep | Requests | Errors | req/s | p50 ms | p95 ms | p99 ms | Peak serving RSS MiB |
|---|---|---|---|---|---|---|---|---|---|---|
| workerman | 4 | 1 | 1 | 5000 | 0 | 331 | 2.63 | 5.26 | 8.50 | 190.2 |
| frankenphp | 4 | 1 | 1 | 5000 | 0 | 248 | 3.47 | 7.47 | 12.25 | 143.7 |
| workerman-shipped | 4 | 1 | 1 | 5000 | 0 | 293 | 2.95 | 6.10 | 9.79 | 175.2 |
| workerman | 4 | 8 | 1 | 5000 | 0 | 1257 | 5.28 | 14.36 | 20.80 | 192.9 |
| frankenphp | 4 | 8 | 1 | 5000 | 0 | 1293 | 5.60 | 10.46 | 14.49 | 137.3 |
| workerman-shipped | 4 | 8 | 1 | 5000 | 2 | 783 | 7.81 | 24.51 | 42.24 | 186.8 |
| workerman-shipped | 4 | 1 | 2 | 5000 | 0 | 264 | 3.20 | 7.17 | 10.44 | 174.7 |
| frankenphp | 4 | 1 | 2 | 5000 | 0 | 231 | 3.69 | 8.21 | 12.88 | 124.5 |
| workerman | 4 | 1 | 2 | 5000 | 0 | 445 | 2.00 | 3.49 | 5.49 | 182.0 |
| workerman-shipped | 4 | 8 | 2 | 5000 | 0 | 1023 | 6.21 | 17.85 | 27.87 | 187.1 |
| frankenphp | 4 | 8 | 2 | 5000 | 0 | 1098 | 5.77 | 15.46 | 28.92 | 94.6 |
| workerman | 4 | 8 | 2 | 5000 | 0 | 929 | 6.75 | 19.98 | 34.83 | 192.0 |
| workerman | 4 | 1 | 3 | 5000 | 0 | 324 | 2.62 | 5.87 | 9.83 | 180.5 |
| frankenphp | 4 | 1 | 3 | 5000 | 0 | 257 | 3.26 | 7.82 | 11.75 | 94.2 |
| workerman-shipped | 4 | 1 | 3 | 5000 | 0 | 286 | 2.83 | 6.98 | 10.05 | 179.5 |
| workerman | 4 | 8 | 3 | 5000 | 0 | 1023 | 6.41 | 16.35 | 24.32 | 181.1 |
| frankenphp | 4 | 8 | 3 | 5000 | 0 | 916 | 6.87 | 20.18 | 33.13 | 92.8 |
| workerman-shipped | 4 | 8 | 3 | 5000 | 0 | 920 | 6.85 | 19.63 | 27.72 | 179.5 |

### Pass B

Configuration: hosts workerman, frankenphp, workerman-shipped; 3 repetitions; image `sha256:12fc45cfbfd9a25f1718cee251c667e47e3f69a8da97ea347a14c9aea02f19f9` built from tree `0d97b14f32c2662c8c2fcb282b434617968b0c75`; working tree at measurement `0d97b14f32c2662c8c2fcb282b434617968b0c75`.

### Median across 3 runs

| Host | Runtime | Workers | Conc. | Requests/run | Errors (total) | req/s | p50 ms | p95 ms | p99 ms | Peak serving RSS MiB |
|---|---|---|---|---|---|---|---|---|---|---|
| workerman | Workerman v5.2.3, PHP 8.4.27 | 4 | 1 | 5000 | 0 | 221 | 3.62 | 8.26 | 13.59 | 181.8 |
| frankenphp | Laravel Octane v2.20.0 on FrankenPHP v1.13.5 PHP 8.4.27 Caddy v2.11.9 h1:bhZtcRKQOq0fuLvEgCdPYvUxXyGn9kzjG4lh2332DTE=, PHP 8.4.27 | 4 | 1 | 5000 | 0 | 298 | 3.05 | 5.39 | 8.21 | 97.4 |
| workerman-shipped | Workerman v5.2.3, PHP 8.4.27 | 4 | 1 | 5000 | 0 | 279 | 2.84 | 6.84 | 13.86 | 180.2 |
| workerman | Workerman v5.2.3, PHP 8.4.27 | 4 | 8 | 5000 | 1 | 754 | 8.20 | 25.13 | 40.78 | 193.2 |
| frankenphp | Laravel Octane v2.20.0 on FrankenPHP v1.13.5 PHP 8.4.27 Caddy v2.11.9 h1:bhZtcRKQOq0fuLvEgCdPYvUxXyGn9kzjG4lh2332DTE=, PHP 8.4.27 | 4 | 8 | 5000 | 0 | 924 | 7.73 | 14.13 | 19.82 | 98.0 |
| workerman-shipped | Workerman v5.2.3, PHP 8.4.27 | 4 | 8 | 5000 | 0 | 629 | 10.05 | 29.09 | 48.36 | 186.8 |

### Per-run results

| Host | Workers | Conc. | Rep | Requests | Errors | req/s | p50 ms | p95 ms | p99 ms | Peak serving RSS MiB |
|---|---|---|---|---|---|---|---|---|---|---|
| workerman | 4 | 1 | 1 | 5000 | 0 | 216 | 4.03 | 8.26 | 13.59 | 185.9 |
| frankenphp | 4 | 1 | 1 | 5000 | 0 | 205 | 4.32 | 8.55 | 13.24 | 145.1 |
| workerman-shipped | 4 | 1 | 1 | 5000 | 0 | 150 | 5.43 | 13.27 | 21.62 | 180.2 |
| workerman | 4 | 8 | 1 | 5000 | 0 | 893 | 7.06 | 19.69 | 32.69 | 193.7 |
| frankenphp | 4 | 8 | 1 | 5000 | 0 | 924 | 7.87 | 14.13 | 19.82 | 142.3 |
| workerman-shipped | 4 | 8 | 1 | 5000 | 0 | 629 | 10.05 | 29.09 | 48.36 | 186.9 |
| workerman-shipped | 4 | 1 | 2 | 5000 | 0 | 279 | 2.84 | 6.84 | 13.86 | 175.4 |
| frankenphp | 4 | 1 | 2 | 5000 | 0 | 298 | 3.05 | 5.39 | 8.21 | 97.4 |
| workerman | 4 | 1 | 2 | 5000 | 0 | 221 | 3.62 | 9.35 | 16.18 | 181.8 |
| workerman-shipped | 4 | 8 | 2 | 5000 | 0 | 1022 | 6.14 | 16.85 | 31.94 | 186.8 |
| frankenphp | 4 | 8 | 2 | 5000 | 0 | 1262 | 5.89 | 9.83 | 12.31 | 98.0 |
| workerman | 4 | 8 | 2 | 5000 | 1 | 537 | 9.16 | 33.21 | 82.45 | 192.7 |
| workerman | 4 | 1 | 3 | 5000 | 0 | 258 | 3.31 | 6.91 | 11.11 | 169.3 |
| frankenphp | 4 | 1 | 3 | 5000 | 0 | 306 | 3.04 | 5.02 | 7.81 | 97.3 |
| workerman-shipped | 4 | 1 | 3 | 5000 | 0 | 339 | 2.76 | 4.44 | 7.14 | 186.6 |
| workerman | 4 | 8 | 3 | 5000 | 0 | 754 | 8.20 | 25.13 | 40.78 | 193.2 |
| frankenphp | 4 | 8 | 3 | 5000 | 0 | 918 | 7.73 | 15.33 | 22.08 | 97.9 |
| workerman-shipped | 4 | 8 | 3 | 5000 | 0 | 256 | 17.24 | 97.47 | 202.59 | 186.7 |

### Pass C

Configuration: hosts workerman, frankenphp; 7 repetitions; image `sha256:12fc45cfbfd9a25f1718cee251c667e47e3f69a8da97ea347a14c9aea02f19f9` built from tree `0d97b14f32c2662c8c2fcb282b434617968b0c75`; working tree at measurement `dcbc4a5846224bf1010474b8cd2f8fa4ee87bc62`.

### Median across 7 runs

| Host | Runtime | Workers | Conc. | Requests/run | Errors (total) | req/s | p50 ms | p95 ms | p99 ms | Peak serving RSS MiB |
|---|---|---|---|---|---|---|---|---|---|---|
| workerman | Workerman v5.2.3, PHP 8.4.27 | 4 | 1 | 5000 | 0 | 140 | 5.90 | 14.40 | 22.58 | 181.4 |
| frankenphp | Laravel Octane v2.20.0 on FrankenPHP v1.13.5 PHP 8.4.27 Caddy v2.11.9 h1:bhZtcRKQOq0fuLvEgCdPYvUxXyGn9kzjG4lh2332DTE=, PHP 8.4.27 | 4 | 1 | 5000 | 0 | 120 | 7.03 | 16.52 | 26.74 | 94.3 |
| workerman | Workerman v5.2.3, PHP 8.4.27 | 4 | 8 | 5000 | 4 | 545 | 11.51 | 32.52 | 58.87 | 192.3 |
| frankenphp | Laravel Octane v2.20.0 on FrankenPHP v1.13.5 PHP 8.4.27 Caddy v2.11.9 h1:bhZtcRKQOq0fuLvEgCdPYvUxXyGn9kzjG4lh2332DTE=, PHP 8.4.27 | 4 | 8 | 5000 | 0 | 750 | 9.11 | 20.93 | 31.82 | 94.6 |

### Per-run results

| Host | Workers | Conc. | Rep | Requests | Errors | req/s | p50 ms | p95 ms | p99 ms | Peak serving RSS MiB |
|---|---|---|---|---|---|---|---|---|---|---|
| workerman | 4 | 1 | 1 | 5000 | 0 | 122 | 6.89 | 15.97 | 24.36 | 185.0 |
| frankenphp | 4 | 1 | 1 | 5000 | 0 | 106 | 7.98 | 18.89 | 29.49 | 143.2 |
| workerman | 4 | 8 | 1 | 5000 | 0 | 399 | 15.66 | 46.08 | 75.84 | 184.8 |
| frankenphp | 4 | 8 | 1 | 5000 | 0 | 430 | 16.78 | 33.02 | 49.65 | 135.3 |
| frankenphp | 4 | 1 | 2 | 5000 | 0 | 94 | 8.58 | 22.36 | 35.89 | 124.1 |
| workerman | 4 | 1 | 2 | 5000 | 0 | 140 | 5.90 | 14.40 | 24.87 | 186.5 |
| frankenphp | 4 | 8 | 2 | 5000 | 0 | 575 | 11.95 | 25.58 | 39.07 | 108.9 |
| workerman | 4 | 8 | 2 | 5000 | 3 | 382 | 15.52 | 51.23 | 98.46 | 192.2 |
| workerman | 4 | 1 | 3 | 5000 | 0 | 126 | 6.84 | 15.23 | 22.58 | 180.2 |
| frankenphp | 4 | 1 | 3 | 5000 | 0 | 113 | 7.30 | 17.79 | 26.74 | 93.4 |
| workerman | 4 | 8 | 3 | 5000 | 0 | 348 | 18.54 | 51.84 | 77.77 | 193.4 |
| frankenphp | 4 | 8 | 3 | 5000 | 0 | 515 | 13.19 | 26.92 | 38.60 | 94.4 |
| frankenphp | 4 | 1 | 4 | 5000 | 0 | 120 | 7.03 | 16.09 | 25.57 | 94.4 |
| workerman | 4 | 1 | 4 | 5000 | 0 | 101 | 7.73 | 22.27 | 37.14 | 181.4 |
| frankenphp | 4 | 8 | 4 | 5000 | 0 | 819 | 8.35 | 19.51 | 28.88 | 94.5 |
| workerman | 4 | 8 | 4 | 5000 | 0 | 675 | 8.88 | 29.81 | 51.77 | 193.5 |
| workerman | 4 | 1 | 5 | 5000 | 0 | 302 | 2.66 | 6.72 | 13.44 | 192.3 |
| frankenphp | 4 | 1 | 5 | 5000 | 0 | 159 | 4.11 | 16.52 | 31.03 | 93.9 |
| workerman | 4 | 8 | 5 | 5000 | 0 | 545 | 11.51 | 32.52 | 58.87 | 194.0 |
| frankenphp | 4 | 8 | 5 | 5000 | 0 | 750 | 9.11 | 20.93 | 31.82 | 94.6 |
| frankenphp | 4 | 1 | 6 | 5000 | 0 | 283 | 3.18 | 5.95 | 10.15 | 94.3 |
| workerman | 4 | 1 | 6 | 5000 | 0 | 253 | 3.01 | 7.13 | 15.90 | 181.4 |
| frankenphp | 4 | 8 | 6 | 5000 | 0 | 1141 | 6.42 | 11.16 | 15.03 | 95.0 |
| workerman | 4 | 8 | 6 | 5000 | 0 | 1246 | 5.24 | 12.85 | 18.45 | 192.3 |
| workerman | 4 | 1 | 7 | 5000 | 0 | 200 | 3.69 | 11.32 | 21.11 | 180.8 |
| frankenphp | 4 | 1 | 7 | 5000 | 0 | 189 | 4.13 | 11.20 | 19.16 | 94.1 |
| workerman | 4 | 8 | 7 | 5000 | 1 | 1110 | 5.98 | 15.57 | 23.18 | 191.7 |
| frankenphp | 4 | 8 | 7 | 5000 | 0 | 963 | 7.21 | 15.48 | 25.36 | 94.2 |


## Docker commands executed in the headline pass

```text
'docker' 'network' 'create' 'gw-bench-<id>-net'
'docker' 'run' '-d' '--name' 'gw-bench-<id>-pg' '--network' 'gw-bench-<id>-net' '-e' 'POSTGRES_PASSWORD=bench' 'postgres:18.2' '-c' 'max_connections=300'
'docker' 'exec' '-e' 'PGPASSWORD=bench' 'gw-bench-<id>-pg' 'psql' '-U' 'postgres' '-h' '127.0.0.1' '-d' 'postgres' '-tAX' '-v' 'ON_ERROR_STOP=1' '-c' 'CREATE DATABASE bench_template'
'docker' 'exec' '-e' 'PGPASSWORD=bench' 'gw-bench-<id>-pg' 'psql' '-U' 'postgres' '-h' '127.0.0.1' '-d' 'bench_template' '-tAX' '-v' 'ON_ERROR_STOP=1' '-c' 'SELECT current_database() || '\''|'\'' || inet_server_port() || '\''|'\'' || pg_postmaster_start_time()::text'
'docker' 'run' '--rm' '--network' 'gw-bench-<id>-net' '-e' 'APP_KEY=base64:<generated per run>' '-e' 'GATEWAY_INTERNAL_TOKEN=bench-internal-token' '-e' 'REGISTRY_TOKEN=bench-registry-token' '-e' 'DB_CONNECTION=pgsql' '-e' 'DB_URL=postgresql://postgres:bench@gw-bench-<id>-pg:5432/bench_template' '-e' 'DB_DATABASE=bench_template' '-e' 'GATEWAY_PATH_RESOLVER=Gateway\Router\BenchHttpSmokePathResolver' '-e' 'GATEWAY_PLANNER_WORKERS=4' '-e' 'GATEWAY_PLANNER_MAX_REQUESTS=500' '-v' '/Users/webong/Documents/Codex/2026-11-03/gateway-framework-neutral-migrations/scripts/bench/BenchHttpSmokePathResolver.php:/gateway/app/BenchHttpSmokePathResolver.php:ro' '--entrypoint' 'php' 'gateway-planner-bench:local' '/gateway/tests/Support/resolve-planner-database.php'
'docker' 'run' '--rm' '--network' 'gw-bench-<id>-net' '-e' 'APP_KEY=base64:<generated per run>' '-e' 'GATEWAY_INTERNAL_TOKEN=bench-internal-token' '-e' 'REGISTRY_TOKEN=bench-registry-token' '-e' 'DB_CONNECTION=pgsql' '-e' 'DB_URL=postgresql://postgres:bench@gw-bench-<id>-pg:5432/bench_template' '-e' 'DB_DATABASE=bench_template' '-e' 'GATEWAY_PATH_RESOLVER=Gateway\Router\BenchHttpSmokePathResolver' '-e' 'GATEWAY_PLANNER_WORKERS=4' '-e' 'GATEWAY_PLANNER_MAX_REQUESTS=500' '-v' '/Users/webong/Documents/Codex/2026-11-03/gateway-framework-neutral-migrations/scripts/bench/BenchHttpSmokePathResolver.php:/gateway/app/BenchHttpSmokePathResolver.php:ro' 'gateway-planner-bench:local' 'migrate'
'docker' 'run' '--rm' '--network' 'gw-bench-<id>-net' '-e' 'APP_KEY=base64:<generated per run>' '-e' 'GATEWAY_INTERNAL_TOKEN=bench-internal-token' '-e' 'REGISTRY_TOKEN=bench-registry-token' '-e' 'DB_CONNECTION=pgsql' '-e' 'DB_URL=postgresql://postgres:bench@gw-bench-<id>-pg:5432/bench_template' '-e' 'DB_DATABASE=bench_template' '-e' 'GATEWAY_PATH_RESOLVER=Gateway\Router\BenchHttpSmokePathResolver' '-e' 'GATEWAY_PLANNER_WORKERS=4' '-e' 'GATEWAY_PLANNER_MAX_REQUESTS=500' '-v' '/Users/webong/Documents/Codex/2026-11-03/gateway-framework-neutral-migrations/scripts/bench/BenchHttpSmokePathResolver.php:/gateway/app/BenchHttpSmokePathResolver.php:ro' '--entrypoint' 'php' 'gateway-planner-bench:local' '/gateway/tests/Support/resolve-planner-database.php'
'docker' 'run' '--rm' '--network' 'gw-bench-<id>-net' '-e' 'APP_KEY=base64:<generated per run>' '-e' 'GATEWAY_INTERNAL_TOKEN=bench-internal-token' '-e' 'REGISTRY_TOKEN=bench-registry-token' '-e' 'DB_CONNECTION=pgsql' '-e' 'DB_URL=postgresql://postgres:bench@gw-bench-<id>-pg:5432/bench_template' '-e' 'DB_DATABASE=bench_template' '-e' 'GATEWAY_PATH_RESOLVER=Gateway\Router\BenchHttpSmokePathResolver' '-e' 'GATEWAY_PLANNER_WORKERS=4' '-e' 'GATEWAY_PLANNER_MAX_REQUESTS=500' '-v' '/Users/webong/Documents/Codex/2026-11-03/gateway-framework-neutral-migrations/scripts/bench/BenchHttpSmokePathResolver.php:/gateway/app/BenchHttpSmokePathResolver.php:ro' '--entrypoint' 'php' '-e' 'GATEWAY_SEED_ENDPOINT_KEY=http-smoke-endpoint' '-e' 'GATEWAY_SEED_ROUTING_SCOPE=application' '-e' 'GATEWAY_SEED_ROUTING_KEY=app-123' '-e' 'GATEWAY_SEED_SUBSCRIBER_ID=workspace-42' '-e' 'GATEWAY_SEED_URL=https://subscriber.example.test/hook' 'gateway-planner-bench:local' '/gateway/tests/Support/seed-planner-endpoint.php'
'docker' 'exec' '-e' 'PGPASSWORD=bench' 'gw-bench-<id>-pg' 'psql' '-U' 'postgres' '-h' '127.0.0.1' '-d' 'bench_template' '-tAX' '-v' 'ON_ERROR_STOP=1' '-c' 'SELECT (SELECT count(*) FROM endpoints) || '\''|'\'' || (SELECT count(*) FROM subscriptions)'
'docker' 'exec' '-e' 'PGPASSWORD=bench' 'gw-bench-<id>-pg' 'psql' '-U' 'postgres' '-h' '127.0.0.1' '-d' 'postgres' '-tAX' '-v' 'ON_ERROR_STOP=1' '-c' 'CREATE DATABASE bench_workerman TEMPLATE bench_template'
'docker' 'run' '--rm' '--network' 'gw-bench-<id>-net' '-e' 'APP_KEY=base64:<generated per run>' '-e' 'GATEWAY_INTERNAL_TOKEN=bench-internal-token' '-e' 'REGISTRY_TOKEN=bench-registry-token' '-e' 'DB_CONNECTION=pgsql' '-e' 'DB_URL=postgresql://postgres:bench@gw-bench-<id>-pg:5432/bench_workerman' '-e' 'DB_DATABASE=bench_workerman' '-e' 'GATEWAY_PATH_RESOLVER=Gateway\Router\BenchHttpSmokePathResolver' '-e' 'GATEWAY_PLANNER_WORKERS=4' '-e' 'GATEWAY_PLANNER_MAX_REQUESTS=500' '-v' '/Users/webong/Documents/Codex/2026-11-03/gateway-framework-neutral-migrations/scripts/bench/BenchHttpSmokePathResolver.php:/gateway/app/BenchHttpSmokePathResolver.php:ro' '--entrypoint' 'php' 'gateway-planner-bench:local' '/gateway/tests/Support/resolve-planner-database.php'
'docker' 'exec' '-e' 'PGPASSWORD=bench' 'gw-bench-<id>-pg' 'psql' '-U' 'postgres' '-h' '127.0.0.1' '-d' 'bench_workerman' '-tAX' '-v' 'ON_ERROR_STOP=1' '-c' 'SELECT current_database() || '\''|'\'' || inet_server_port() || '\''|'\'' || pg_postmaster_start_time()::text'
'docker' 'run' '-d' '--name' 'gw-bench-<id>-workerman' '--network' 'gw-bench-<id>-net' '--cpus' '4' '--memory' '2g' '--memory-swap' '2g' '-p' '127.0.0.1::8080' '-e' 'APP_KEY=base64:<generated per run>' '-e' 'GATEWAY_INTERNAL_TOKEN=bench-internal-token' '-e' 'REGISTRY_TOKEN=bench-registry-token' '-e' 'DB_CONNECTION=pgsql' '-e' 'DB_URL=postgresql://postgres:bench@gw-bench-<id>-pg:5432/bench_workerman' '-e' 'DB_DATABASE=bench_workerman' '-e' 'GATEWAY_PATH_RESOLVER=Gateway\Router\BenchHttpSmokePathResolver' '-e' 'GATEWAY_PLANNER_WORKERS=4' '-e' 'GATEWAY_PLANNER_MAX_REQUESTS=500' '-e' 'GATEWAY_PLANNER_SERVER=workerman' '-v' '/Users/webong/Documents/Codex/2026-11-03/gateway-framework-neutral-migrations/scripts/bench/BenchHttpSmokePathResolver.php:/gateway/app/BenchHttpSmokePathResolver.php:ro' '-v' '/Users/webong/Documents/Codex/2026-11-03/gateway-framework-neutral-migrations/scripts/bench/php-bench.ini:/usr/local/etc/php/conf.d/zz-bench.ini:ro' '-v' '/Users/webong/Documents/Codex/2026-11-03/gateway-framework-neutral-migrations/scripts/bench/php-bench.ini:/opt/frankenphp/conf.d/zz-bench.ini:ro' 'gateway-planner-bench:local' 'serve'
'docker' 'port' 'gw-bench-<id>-workerman' '8080/tcp'
'docker' 'exec' 'gw-bench-<id>-workerman' 'php' '/gateway/tests/Support/resolve-planner-database.php'
'docker' 'exec' '-e' 'PGPASSWORD=bench' 'gw-bench-<id>-pg' 'psql' '-U' 'postgres' '-h' '127.0.0.1' '-d' 'bench_workerman' '-tAX' '-v' 'ON_ERROR_STOP=1' '-c' 'SELECT current_database() || '\''|'\'' || inet_server_port() || '\''|'\'' || pg_postmaster_start_time()::text'
'docker' 'exec' 'gw-bench-<id>-workerman' 'php' '-r' 'echo PHP_VERSION;'
'docker' 'exec' 'gw-bench-<id>-workerman' 'php' '-r' '$installed = json_decode(file_get_contents('\''/gateway/vendor/composer/installed.json'\''), true);
$wanted = ['\''laravel/framework'\'', '\''laravel/octane'\'', '\''workerman/workerman'\'', '\''webong/gateway'\''];
$out = [];
foreach ($installed['\''packages'\''] ?? $installed as $package) {
    if (in_array($package['\''name'\''], $wanted, true)) { $out[$package['\''name'\'']] = $package['\''version'\'']; }
}
echo json_encode($out);'
'docker' 'exec' 'gw-bench-<id>-workerman' 'php' '-r' 'echo json_encode(array_combine(array (
  0 => '\''memory_limit'\'',
  1 => '\''opcache.enable'\'',
  2 => '\''opcache.enable_cli'\'',
  3 => '\''opcache.memory_consumption'\'',
  4 => '\''opcache.validate_timestamps'\'',
  5 => '\''opcache.jit'\'',
  6 => '\''zend.assertions'\'',
), array_map(static fn ($k) => (string) ini_get($k), array (
  0 => '\''memory_limit'\'',
  1 => '\''opcache.enable'\'',
  2 => '\''opcache.enable_cli'\'',
  3 => '\''opcache.memory_consumption'\'',
  4 => '\''opcache.validate_timestamps'\'',
  5 => '\''opcache.jit'\'',
  6 => '\''zend.assertions'\'',
))));'
'docker' 'exec' '-e' 'PGPASSWORD=bench' 'gw-bench-<id>-pg' 'psql' '-U' 'postgres' '-h' '127.0.0.1' '-d' 'postgres' '-tAX' '-v' 'ON_ERROR_STOP=1' '-c' 'CREATE DATABASE bench_frankenphp TEMPLATE bench_template'
'docker' 'run' '--rm' '--network' 'gw-bench-<id>-net' '-e' 'APP_KEY=base64:<generated per run>' '-e' 'GATEWAY_INTERNAL_TOKEN=bench-internal-token' '-e' 'REGISTRY_TOKEN=bench-registry-token' '-e' 'DB_CONNECTION=pgsql' '-e' 'DB_URL=postgresql://postgres:bench@gw-bench-<id>-pg:5432/bench_frankenphp' '-e' 'DB_DATABASE=bench_frankenphp' '-e' 'GATEWAY_PATH_RESOLVER=Gateway\Router\BenchHttpSmokePathResolver' '-e' 'GATEWAY_PLANNER_WORKERS=4' '-e' 'GATEWAY_PLANNER_MAX_REQUESTS=500' '-v' '/Users/webong/Documents/Codex/2026-11-03/gateway-framework-neutral-migrations/scripts/bench/BenchHttpSmokePathResolver.php:/gateway/app/BenchHttpSmokePathResolver.php:ro' '--entrypoint' 'php' 'gateway-planner-bench:local' '/gateway/tests/Support/resolve-planner-database.php'
'docker' 'exec' '-e' 'PGPASSWORD=bench' 'gw-bench-<id>-pg' 'psql' '-U' 'postgres' '-h' '127.0.0.1' '-d' 'bench_frankenphp' '-tAX' '-v' 'ON_ERROR_STOP=1' '-c' 'SELECT current_database() || '\''|'\'' || inet_server_port() || '\''|'\'' || pg_postmaster_start_time()::text'
'docker' 'run' '-d' '--name' 'gw-bench-<id>-frankenphp' '--network' 'gw-bench-<id>-net' '--cpus' '4' '--memory' '2g' '--memory-swap' '2g' '-p' '127.0.0.1::8080' '-e' 'APP_KEY=base64:<generated per run>' '-e' 'GATEWAY_INTERNAL_TOKEN=bench-internal-token' '-e' 'REGISTRY_TOKEN=bench-registry-token' '-e' 'DB_CONNECTION=pgsql' '-e' 'DB_URL=postgresql://postgres:bench@gw-bench-<id>-pg:5432/bench_frankenphp' '-e' 'DB_DATABASE=bench_frankenphp' '-e' 'GATEWAY_PATH_RESOLVER=Gateway\Router\BenchHttpSmokePathResolver' '-e' 'GATEWAY_PLANNER_WORKERS=4' '-e' 'GATEWAY_PLANNER_MAX_REQUESTS=500' '-e' 'GATEWAY_PLANNER_SERVER=frankenphp' '-v' '/Users/webong/Documents/Codex/2026-11-03/gateway-framework-neutral-migrations/scripts/bench/BenchHttpSmokePathResolver.php:/gateway/app/BenchHttpSmokePathResolver.php:ro' '-v' '/Users/webong/Documents/Codex/2026-11-03/gateway-framework-neutral-migrations/scripts/bench/php-bench.ini:/usr/local/etc/php/conf.d/zz-bench.ini:ro' '-v' '/Users/webong/Documents/Codex/2026-11-03/gateway-framework-neutral-migrations/scripts/bench/php-bench.ini:/opt/frankenphp/conf.d/zz-bench.ini:ro' 'gateway-planner-bench:local' 'serve'
'docker' 'port' 'gw-bench-<id>-frankenphp' '8080/tcp'
'docker' 'exec' 'gw-bench-<id>-frankenphp' 'php' '/gateway/tests/Support/resolve-planner-database.php'
'docker' 'exec' '-e' 'PGPASSWORD=bench' 'gw-bench-<id>-pg' 'psql' '-U' 'postgres' '-h' '127.0.0.1' '-d' 'bench_frankenphp' '-tAX' '-v' 'ON_ERROR_STOP=1' '-c' 'SELECT current_database() || '\''|'\'' || inet_server_port() || '\''|'\'' || pg_postmaster_start_time()::text'
'docker' 'exec' 'gw-bench-<id>-frankenphp' 'php' '-r' 'echo PHP_VERSION;'
'docker' 'exec' 'gw-bench-<id>-frankenphp' 'php' '-r' '$installed = json_decode(file_get_contents('\''/gateway/vendor/composer/installed.json'\''), true);
$wanted = ['\''laravel/framework'\'', '\''laravel/octane'\'', '\''workerman/workerman'\'', '\''webong/gateway'\''];
$out = [];
foreach ($installed['\''packages'\''] ?? $installed as $package) {
    if (in_array($package['\''name'\''], $wanted, true)) { $out[$package['\''name'\'']] = $package['\''version'\'']; }
}
echo json_encode($out);'
'docker' 'exec' 'gw-bench-<id>-frankenphp' 'frankenphp' 'version'
'docker' 'exec' 'gw-bench-<id>-frankenphp' 'frankenphp' 'php-cli' '-r' 'echo json_encode(array_combine(array (
  0 => '\''memory_limit'\'',
  1 => '\''opcache.enable'\'',
  2 => '\''opcache.enable_cli'\'',
  3 => '\''opcache.memory_consumption'\'',
  4 => '\''opcache.validate_timestamps'\'',
  5 => '\''opcache.jit'\'',
  6 => '\''zend.assertions'\'',
), array_map(static fn ($k) => (string) ini_get($k), array (
  0 => '\''memory_limit'\'',
  1 => '\''opcache.enable'\'',
  2 => '\''opcache.enable_cli'\'',
  3 => '\''opcache.memory_consumption'\'',
  4 => '\''opcache.validate_timestamps'\'',
  5 => '\''opcache.jit'\'',
  6 => '\''zend.assertions'\'',
))));'
'docker' 'exec' '-e' 'PGPASSWORD=bench' 'gw-bench-<id>-pg' 'psql' '-U' 'postgres' '-h' '127.0.0.1' '-d' 'postgres' '-tAX' '-v' 'ON_ERROR_STOP=1' '-c' 'SHOW server_version'
```

_Tested tree for the headline pass: `533f7facbac376afd781a9c1a0ea072408f86611` (fix: restore Planner CI and migrate bundled Router schema) plus the uncommitted changes below; this document was added after the measurements._

```text
M .github/workflows/ci.yml
 M Makefile
 M app/WorkermanLaravelHandler.php
 M app/workerman.php
 M infra/docker/Gateway.planner.Dockerfile
 M pkg/planner/README.md
 M pkg/planner/src/Laravel/Console/LaravelGatewayMigrateCommand.php
 M scripts/bench-planner-workerman.php
 M scripts/planner-start
 M tests/Feature/GatewayMigrationCommandTest.php
 M tests/Feature/WorkermanPlannerTest.php
 M tests/Support/WorkermanPlannerHelper.php
 M tests/Support/seed-planner-endpoint.php
 M tests/Unit/WorkermanPersistentWorkerTest.php
?? docs/verification/planner-host-benchmark-workerman-vs-octane-2026-11-11.md
?? docs/verification/planner-workerman-graceful-shutdown-2026-11-12.md
?? scripts/test-planner-workerman-shutdown
?? tests/Fixtures/InstrumentedHttpSmokePathResolver.php
?? tests/Support/resolve-planner-database.php
```
