# Workerman worker recycling and keep-alive clients

Investigated 2026-11-13. This follows the host benchmark (`planner-host-benchmark-workerman-vs-octane-2026-11-11.md`), where Workerman had a few client-visible failures ("transport: Server returned nothing (no headers, no data)") at concurrency 8 and FrankenPHP had none.

## Symptom

When a worker reached `GATEWAY_PLANNER_MAX_REQUESTS` and recycled, a keep-alive client that held a connection to that worker could get an empty reply instead of a response. The benchmark saw about 5 in 140000 requests; the explanation then ("Workerman closes a recycled worker's connections when it recycles") fitted the pattern but was not isolated by an experiment.

## Cause

`app/workerman.php` called `Worker::stopAll()` right after the response that reached the threshold. In a worker process (Workerman 5.2.3) `stopAll()` calls `Worker::stop(false)`, which:

- closes every connection the worker holds that has no buffered input (an idle connection, or one whose next request has not been read yet, even if the client has already sent it), and
- clears `onMessage`, so a request still waiting in a connection's socket is never read.

Workerman keeps a client's connection in the worker that accepted it, so a client sees the failure in two ways: a request already sent on a second connection of the recycled worker is dropped, and a client that reuses a connection the server has just closed gets an empty reply. The response that reached the threshold was also sent as keep-alive, so its client had no way to know the connection was about to close.

## Verification before the fix

Both tests are in `tests/Feature/WorkermanPlannerTest.php` and use real `app/workerman.php` processes over HTTP with a disposable SQLite database.

- **Lock-step keep-alive clients.** Eight clients over two workers, 30 rounds (240 requests), each round all clients send at once and read their replies. A client behaves like curl: before reusing a connection it drops one the server has already closed, and it honours `Connection: close`. A request without a complete reply counts as a failure; none is retried.

  | Worker threshold | Failed requests out of 240 |
  |---|---|
  | 1,000,000 (never recycles; control) | 0 |
  | 12 (about ten recycles per worker), run 1 | 24 |
  | 12, run 2 | 32 |
  | 12, run 3 | 41 |

- **Pending request on a second connection.** One worker with a threshold of 3. Two clients each complete one request; a third request is held in the handler; while it is held the second client sends its next request, which the worker cannot read yet. When the held request completes the worker recycles. On the previous code the second client got no reply. This test does not depend on timing.

The same client loop against the high threshold shows the harness itself produces no failures, so the failures come from recycling.

## Fix

`app/workerman.php`: a worker that reaches its request limit drains before it stops.

1. It stops accepting connections (`unlisten()`). The master keeps the listening socket open, so new connections go to the other workers, or wait in the backlog for the replacement.
2. Every response from then on, starting with the one that reached the threshold, carries `Connection: close` and is sent with `TcpConnection::end()`. `end()` flushes the reply, shuts down the write side and discards what the client still sends, so the reply is not lost to a reset if a pipelined request arrives behind it. A request already waiting on another connection is still read and answered.
3. A 10 ms timer calls `Worker::stopAll()` once the worker has no connections left, or after a 2 second drain window. A connection that stays silent for the whole window is closed idle at that point.

`onWorkerStop` is unchanged: on `SIGQUIT` it still closes every connection, so the graceful-stop behaviour described in `planner-workerman-graceful-shutdown-2026-11-12.md` is the same. A stop signal during a drain makes the drain timer stand down.

`TcpConnection::end()` exists in every Workerman release the `^5.2` constraint allows (checked against v5.2.0 to v5.2.3).

## Result

- Lock-step test, threshold 12: 0 failed requests out of 240 (previously 24, 32 and 41).
- Pending-request test: the second client's request is answered with a complete 200 relay plan; the idle first client's connection is closed by the server within the test's 8 second allowance, and the replacement worker serves the next plan.
- The five recycle-related tests passed in five consecutive runs (client machine load average 4.6 to 8.6, other applications running).
- `tests/Feature/WorkermanPlannerTest.php`: 19 passed, 2 skipped (the PostgreSQL tests, run without a PostgreSQL server). With a disposable PostgreSQL server the full suite had 418 passed, 6 skipped and 3 failed; the 3 failures are in `ExtensionArtifactReconciliationTest` and come from a missing `pkg/hookscript/companion/vendor` directory in the fresh worktree, not from this change.

## Not measured

The benchmark harness has not been re-run with this change, so the effect on the benchmark's failure count and throughput is not measured. To check it, run `php scripts/bench-planner-hosts.php --build`, then `--hosts=workerman,frankenphp` with the benchmark's defaults, and compare the failure counts with the 2026-11-11 passes. Throughput on this shared machine varies several-fold between runs of one configuration, so only the failure count is a meaningful signal.

## Limits

- An idle keep-alive connection cannot be retired without a race: if a client sends a request in the instant the server closes a connection that stayed silent for the whole 2 second window, it still gets an empty reply. That is true of any server that closes idle connections; clients such as curl check for a closed connection before reusing one.
- While a worker drains, it is not replaced, so capacity is reduced until the last connection closes. With clients that keep every connection busy this is a few milliseconds; a client that parks an idle connection holds the worker for the whole 2 second window. With `GATEWAY_PLANNER_WORKERS=1`, new requests wait in the backlog during the drain and the replacement's boot.
- After the closing response, a connection lingers up to Workerman's default 1 second if the client does not hang up.
- The 2 second window is a constant in `app/workerman.php`, not an environment variable.
- Pipelined requests behind the closing response on the same connection are discarded, as HTTP/1.1 allows; the client retries them on a new connection.
- Tested only on macOS (ARM64), PHP 8.5.8, Workerman 5.2.3, loopback.
