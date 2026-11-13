# Workerman graceful shutdown in the Planner container

Investigated 2026-11-12. This follows the launcher change that turns `SIGTERM`/`SIGINT` into Workerman's graceful `SIGQUIT` (`scripts/planner-start`).

## Symptom

With that launcher, `docker stop` on a container serving a held request drained the request, but the container then ran until the stop timeout and was `SIGKILL`ed (exit 137). `docker kill -s TERM`, which delivers the same signal, exited cleanly. An idle container exited cleanly either way.

## Evidence before the fix

`php scripts/bench-planner-hosts.php --shutdown-matrix=stop:0,kill:0,stop:3,kill:3` (one fresh Workerman container per case, 4 workers, `docker stop -t 20`):

| Stop method | Request held | Response | Container exit | Exit code |
|---|---|---|---|---|
| `docker stop` | none | n/a | +0.3 s | 0 |
| `docker kill -s TERM` | none | n/a | +0.6 s | 0 |
| `docker stop` | 3 s | complete, +3.1 s | **+20.4 s** | **137** |
| `docker kill -s TERM` | 3 s | complete, +3.2 s | +3.5 s | 0 |

Workerman's own log showed `received signal SIGQUIT` and `stopping` in the failing case, and never the `remains 0 connection(s)` / `has been stopped` lines the passing cases print.

## Root cause

`strace -f` attached to the master and all four workers (debug image with `strace`, `--cap-add SYS_PTRACE`) before the stop:

- The master received `SIGQUIT` from the launcher (`si_pid=1`) and forwarded it to the four workers.
- Three idle workers exited immediately with status 0.
- The worker that had served the request wrote the response on its client socket (`sendto(7, "HTTP/1.1 200 OK...")`) and then did nothing but poll that socket every 10 ms (`pselect6(8, [7], ...)`) for the rest of the run: no `read`, no `close`. The master meanwhile woke once a second (`SIGALRM`) waiting for it.

Workerman 5.2.3 explains it:

- `Http::encode()` never closes a connection after a response, whatever `Connection` header the request carried; it waits for the client to hang up.
- In a graceful stop `Worker::stop()` skips closing connections, and the child's stop callback exits only when `ConnectionInterface::$statistics['connection_count'] <= 0`. Graceful mode has no stop timeout.

So a graceful stop completes only when every client has closed its connection. Under `docker kill` the test client's hang-up reached the worker; under `docker stop` on this runtime (OrbStack) it evidently did not, which is consistent with the trace (the worker never saw EOF) but was not confirmed at the network level.

That is not specific to this runtime. Any client that keeps an idle keep-alive connection open (a proxy's connection pool, a browser, an HTTP client with keep-alive) holds the worker, the master and the container until the orchestrator's `SIGKILL`. It reproduces on the host with no Docker involved.

## Fix

`app/workerman.php`: the worker's `onWorkerStop` now closes the worker's connections. No request is being handled when a stop is processed (the signal is handled between events, after a response has been written), so everything still open is idle or finished; a connection with a response still buffered is closed once it is flushed. Workerman's non-graceful stop (including recycling after `GATEWAY_PLANNER_MAX_REQUESTS`) already closed these connections, so that path is unchanged in effect.

## Regression tests

- `tests/Feature/WorkermanPlannerTest.php`: "exits on SIGQUIT even while clients keep idle keep-alive connections open". Six clients each complete a request and keep the connection open; the host must exit on `SIGQUIT` within 10 s with status 0. It fails on the previous `app/workerman.php` (the host does not exit within 10 s) and passes with the fix.
- Container level: `php scripts/bench-planner-hosts.php --check-shutdown` holds a 7 s request in a real container and runs `docker stop -t 30`.

| Case (7 s held request, `docker stop`) | Response | Container exit | Exit code |
|---|---|---|---|
| Previous launcher (`exec php workerman.php`) | not completed | +2.4 s | 0 (request lost) |
| New launcher, before the fix | complete | +32 s | 137 |
| New launcher, with the fix | complete at +7.1 s | **+7.3 s** | **0** |

After the fix the matrix cases all drain and exit cleanly: `docker stop` with no request (+0.3 s), with a 3 s request (+3.5 s) and with a 7 s request (+7.3 s), and `docker kill -s TERM` with a 7 s request (+7.7 s).

## Limits

- Tested on one runtime (OrbStack, ARM64). The root cause is Workerman's own behaviour and reproduces on the host, but `docker stop`'s handling of the client connection on other runtimes was not examined.
- Workerman's graceful drain has no upper bound: a request that never finishes holds the stop until the orchestrator's grace period ends. Set `terminationGracePeriodSeconds` / `docker stop -t` to cover the slowest request.
- Debugging aids: `--shutdown-matrix=MODE:HOLD,...` runs the cases above; adding `--strace --image=<image with strace>` attaches `strace` to the master and workers.
