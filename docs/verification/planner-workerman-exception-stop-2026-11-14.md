# Workerman stop after a request that throws (2026-11-14)

A Workerman Planner host started by the test harness did not stop on SIGQUIT once
one request had made the application throw (the host answered 500). The master
and its worker stayed alive until the harness force-killed them after its 15 s
timeout. This note records why, and why the production host is not affected.

## What was observed

With one worker, a request to `/http-smoke/write-then-throw/{token}` (the probe
fixture's route that writes inside a transaction and throws) answered 500. After
SIGQUIT the master logged `stopping` and neither process exited. Inside the worker's
`onWorkerStop`, `$worker->connections` was empty but Workerman's
`ConnectionInterface::$statistics['connection_count']` was still 1, and
`gc_collect_cycles()` freed nothing.

Workerman decrements that count in `TcpConnection::__destruct`, and its graceful
stop (`Worker::stopAll`) polls it until it reaches 0, so a single connection
object that something still references keeps the worker, the master and, in a
container, the orchestrator waiting. It reproduced on SQLite, so it is not a
database effect.

## Why

A breadth-first walk of the worker's object graph (the handler and the static
properties of every declared class, including the private properties of parent
classes, which `ReflectionObject` does not list) found the holder:

```
Illuminate\Foundation\Application::$instance->instances[Illuminate\Testing\LoggedExceptionCollection]
  ->items[0]            the RuntimeException the request threw
  ->trace[48][args][0]  the Workerman Request, which holds its TcpConnection
```

`FoundationServiceProvider::registerExceptionTracking()` registers that collection,
and a `MessageLogged` listener that pushes every logged exception into it, only
`if ($this->app->runningUnitTests())`, that is when `APP_ENV=testing`. The harness
starts the host as a child process that inherits Pest's environment, and
`phpunit.xml.dist` sets `APP_ENV=testing`. Every exception the host logged was
therefore kept, with its stack-trace arguments (the request and its connection), for
the life of the process.

## Production is not affected

The Planner image sets `APP_ENV=production` (`infra/docker/Gateway.planner.Dockerfile`)
and `scripts/router-start` exports it; `app/bootstrap.php` otherwise defaults to
`production`. In that environment the collection is never registered. The same request,
with the same host started as production:

| Host `APP_ENV` | Response | `connection_count` in `onWorkerStop` | After SIGQUIT |
| --- | --- | --- | --- |
| `testing` (inherited) | 500 | 1 | still running after 4 s; force-killed after 15 s |
| `production` | 500 | 0 | exited within 4 s |

The object-graph walk finds no `TcpConnection` outside `$worker->connections` under
`production`. There is no per-error connection leak in the production host, and
`app/workerman.php` and `app/WorkermanLaravelHandler.php` are unchanged.

## What changed

- `gatewayPlannerStartWorkerman()` (`tests/Support/WorkermanPlannerHelper.php`) starts the
  host with `APP_ENV=production`, with a comment saying why.
- The probe fixture reports `app()->environment()` and `runningUnitTests()` from inside the
  host.
- New test `exits on SIGQUIT after a request made the application throw`: checks the host
  reports `production`, sends a request that throws (500), checks the same worker still serves,
  then requires a clean SIGQUIT exit (code 0, not signalled) within 10 s.

Negative controls: with the override removed the test fails at once on the environment
assertion (`'testing'`); with that assertion also removed it fails after 10 s because the
host did not exit.

## Runs

macOS arm64, PHP 8.5.8, `postgres:17` for the PostgreSQL cases.

- `vendor/bin/pest tests/Feature/WorkermanPlannerTest.php tests/Unit/WorkermanPersistentWorkerTest.php`
  with `GATEWAY_TEST_POSTGRES_*`: 29 passed (552 assertions), including the new test.
- Full suite: see the pull request.

## Not covered

The in-process tests in `tests/Unit/WorkermanPersistentWorkerTest.php` still run under
`APP_ENV=testing` (they have no separate host process to stop). A host deliberately started
with `APP_ENV=testing` outside the tests would still hold exceptions; that is Laravel's
documented behaviour for unit tests and is not a supported way to run the Planner.
