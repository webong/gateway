<?php

declare(strict_types=1);

use Webong\Gateway\Migrations\GatewayMigrationRunner;

require_once __DIR__.'/../Support/WorkermanPlannerHelper.php';

/**
 * Real Workerman process integration tests.
 *
 * A genuine `app/servers/workerman/server.php` process is launched for each scenario against a
 * disposable SQLite database that is migrated and seeded with a real endpoint +
 * relay subscription. The process is exercised over plain HTTP: authenticated
 * planning of a database-backed route, authenticated management mutation +
 * readback, worker recycling with an observed PID change, and graceful
 * shutdown. Nothing here touches an existing application database (inherited
 * database and cache settings are neutralised and the resolved database is
 * verified before the first write), and no process other than the one each test
 * launches is signalled.
 */

const WORKERMAN_INTERNAL_TOKEN = 'gateway-internal-test-token';
const WORKERMAN_REGISTRY_TOKEN = 'registry-test-token';
const WORKERMAN_ENDPOINT_KEY = 'http-smoke-endpoint';
const WORKERMAN_SUBSCRIBER_ID = 'workspace-42';
const WORKERMAN_RELAY_URL = 'https://subscriber.example.test/hook';

/** Build the representative planning payload that resolves to the seeded route. */
function workermanPlanBody(string $path = '/http-smoke/registry-ingress'): string
{
    return json_encode([
        'delivery_id' => 'dlr-planner-integration',
        'method' => 'POST',
        'path' => $path,
        'body' => '',
        'headers' => [],
    ], JSON_THROW_ON_ERROR);
}

/** @return array<string, string> */
function workermanPlanHeaders(): array
{
    return ['Content-Type' => 'application/json', 'X-Gateway-Internal' => WORKERMAN_INTERNAL_TOKEN];
}

/**
 * @return array{status: int, body: string, headers: array<string, string>, raw: string, complete: bool, elapsed: float}
 */
function workermanPlan(int $port, string $path = '/http-smoke/registry-ingress'): array
{
    return gatewayPlannerRequest(
        'POST',
        "http://127.0.0.1:$port/_internal/gateway/plan",
        workermanPlanHeaders(),
        workermanPlanBody($path),
    );
}

/** Assert the response is the seeded relay plan, and return its decoded payload. */
function workermanExpectRelayPlan(array $response): array
{
    expect($response['status'])->toBe(200);
    $payload = json_decode($response['body'], true, 512, JSON_THROW_ON_ERROR);
    expect($payload['version'])->toBe('v1')
        ->and($payload['action'])->toBe('relay')
        ->and($payload['relays'])->toHaveCount(1)
        ->and($payload['relays'][0]['subscriber_id'])->toBe(WORKERMAN_SUBSCRIBER_ID)
        ->and($payload['relays'][0]['url'])->toBe(WORKERMAN_RELAY_URL)
        ->and($payload['metadata']['endpoint_key'])->toBe(WORKERMAN_ENDPOINT_KEY);

    return $payload;
}

/**
 * Run a full Workerman scenario against a disposable, seeded database.
 *
 * The data directory is removed and the environment restored on every exit path
 * (setup, startup, readiness and body failures), the host is always stopped
 * through its owned handle, and the scenario fails if the host had to be
 * force-killed rather than exiting on SIGQUIT.
 *
 * @param array<string, string>|callable(string): array<string, string> $extraEnv additional server variables
 *        (workers, max requests); a callable receives the data directory, for variables that name files in it.
 * @param callable(GatewayPlannerHostHandle, int, string): void $body receives the handle, port and data directory
 */
function workermanScenario(array|callable $extraEnv, callable $body): void
{
    gatewayPlannerWithDisposableDirectory(function (string $dataDirectory) use ($extraEnv, $body): void {
        $database = $dataDirectory.'/gateway.sqlite';
        touch($database);
        $extraEnv = is_callable($extraEnv) ? $extraEnv($dataDirectory) : $extraEnv;

        // Build the environment exactly once so every operation (migration, seeding,
        // and the Workerman server) shares the same APP_KEY; the file cache encrypts
        // values with APP_KEY, so a mismatch would produce a MAC-invalid failure.
        $baseEnv = gatewayPlannerBaseEnv($dataDirectory, $database);

        gatewayPlannerWithEnvironment(
            array_merge($baseEnv, $extraEnv),
            function () use ($dataDirectory, $database, $baseEnv, $body): void {
                gatewayPlannerRunMigrations($database);
                gatewayPlannerSeedEndpoint(
                    $baseEnv,
                    WORKERMAN_ENDPOINT_KEY,
                    'application',
                    'app-123',
                    WORKERMAN_RELAY_URL,
                    WORKERMAN_SUBSCRIBER_ID,
                );

                $port = gatewayPlannerFindFreePort();
                $handle = gatewayPlannerStartWorkerman($dataDirectory, $port);
                try {
                    gatewayPlannerWaitForPort($port);
                    $body($handle, $port, $dataDirectory);
                } finally {
                    $stop = gatewayPlannerStopWorkerman($handle);
                }

                if ($stop['forced']) {
                    throw new RuntimeException('The host did not exit on SIGQUIT and had to be force-killed.');
                }
            },
        );
    });
}

it('boots a real Workerman Planner host and serves public health', function (): void {
    workermanScenario([], function (GatewayPlannerHostHandle $handle, int $port): void {
        expect(gatewayPlannerRequest('GET', "http://127.0.0.1:$port/health")['status'])->toBe(204);
    });
});

it('enforces authentication on readiness and planning endpoints', function (): void {
    workermanScenario([], function (GatewayPlannerHostHandle $handle, int $port): void {
        expect(gatewayPlannerRequest('GET', "http://127.0.0.1:$port/_internal/gateway/ready")['status'])->toBe(404);
        expect(gatewayPlannerRequest('GET', "http://127.0.0.1:$port/_internal/gateway/ready", ['X-Gateway-Internal' => 'wrong-token'])['status'])->toBe(404);
        expect(gatewayPlannerRequest('GET', "http://127.0.0.1:$port/_internal/gateway/ready", ['X-Gateway-Internal' => WORKERMAN_INTERNAL_TOKEN])['status'])->toBe(204);

        expect(gatewayPlannerRequest('POST', "http://127.0.0.1:$port/_internal/gateway/plan", ['Content-Type' => 'application/json'], workermanPlanBody())['status'])->toBe(404);
        expect(gatewayPlannerRequest('POST', "http://127.0.0.1:$port/_internal/gateway/plan", ['Content-Type' => 'application/json', 'X-Gateway-Internal' => 'wrong-token'], workermanPlanBody())['status'])->toBe(404);
    });
});

it('plans a seeded database-backed route and returns its bound relay', function (): void {
    workermanScenario([], function (GatewayPlannerHostHandle $handle, int $port): void {
        workermanExpectRelayPlan(workermanPlan($port));
    });
});

it('plans an unbound route as a pass-through', function (): void {
    workermanScenario([], function (GatewayPlannerHostHandle $handle, int $port): void {
        $planned = workermanPlan($port, '/unbound/path');

        expect($planned['status'])->toBe(200);
        $payload = json_decode($planned['body'], true, 512, JSON_THROW_ON_ERROR);
        expect($payload['action'])->toBe('pass_through');
    });
});

it('mutates and reads back a management resource with authorization', function (): void {
    workermanScenario([], function (GatewayPlannerHostHandle $handle, int $port): void {
        $targetBody = json_encode(['name' => 'workerman-managed-target', 'driver' => 'local'], JSON_THROW_ON_ERROR);
        $base = "http://127.0.0.1:$port/targets";

        // Unauthorized rejection (missing and wrong token).
        expect(gatewayPlannerRequest('POST', $base, ['Content-Type' => 'application/json'], $targetBody)['status'])->toBe(401);
        expect(gatewayPlannerRequest('POST', $base, ['Content-Type' => 'application/json', 'Authorization' => 'Bearer wrong-token'], $targetBody)['status'])->toBe(401);

        // Authorized mutation.
        $created = gatewayPlannerRequest('POST', $base, ['Content-Type' => 'application/json', 'Authorization' => 'Bearer '.WORKERMAN_REGISTRY_TOKEN], $targetBody);
        expect($created['status'])->toBe(201);
        $createdId = json_decode($created['body'], true, 512, JSON_THROW_ON_ERROR)['id'] ?? null;
        expect($createdId)->not->toBeNull();

        // Authorized readback contains the mutation.
        $listed = gatewayPlannerRequest('GET', $base, ['Authorization' => 'Bearer '.WORKERMAN_REGISTRY_TOKEN]);
        expect($listed['status'])->toBe(200)
            ->and($listed['body'])->toContain('workerman-managed-target');
    });
});

it('recycles the worker after GATEWAY_PLANNER_MAX_REQUESTS with the master alive', function (): void {
    workermanScenario([
        'GATEWAY_PLANNER_WORKERS' => '1',
        'GATEWAY_PLANNER_MAX_REQUESTS' => '2',
    ], function (GatewayPlannerHostHandle $handle, int $port): void {
        $masterPid = $handle->masterPid;

        // Wait for the single worker to appear.
        $initialPid = null;
        $deadline = microtime(true) + 10.0;
        while (microtime(true) < $deadline) {
            $pids = gatewayPlannerWorkerPids($masterPid);
            if (count($pids) === 1) {
                $initialPid = $pids[0];
                break;
            }
            usleep(20_000);
        }
        expect($initialPid)->not->toBeNull();

        // Drive requests past the recycle threshold. The 2-request threshold
        // triggers Worker::stopAll() in the child, so a replacement worker is
        // spawned; a brief gap (503 while the replacement boots Laravel) is
        // expected and must not fail the test.
        $recycledPid = null;
        for ($request = 0; $request < 40 && $recycledPid === null; $request++) {
            workermanPlan($port);

            foreach (gatewayPlannerWorkerPids($masterPid) as $pid) {
                if ($pid !== $initialPid) {
                    $recycledPid = $pid;
                }
            }
            usleep(20_000);
        }

        // The worker PID must have changed (fails if recycling is disabled) and
        // the master must still be the process we launched.
        expect($recycledPid)->not->toBeNull('worker never recycled after max requests');
        expect(gatewayPlannerPollHost($handle))->toBeTrue();

        // Wait for the replacement worker to be ready, then confirm it serves a
        // valid database-backed plan.
        $after = null;
        $deadline = microtime(true) + 10.0;
        while (microtime(true) < $deadline) {
            $after = workermanPlan($port);
            if ($after['status'] === 200) {
                break;
            }
            usleep(50_000);
        }
        workermanExpectRelayPlan($after);
    });
});

/**
 * Drive keep-alive clients in lock step: every round, each client sends its next request
 * at the same moment, then reads its reply. A client behaves like curl: before reusing a
 * connection it drops one the server has already closed, and it honours `Connection: close`.
 * A request that gets no complete reply is a failure. None is retried.
 *
 * @return array{ok: int, empty: int, other: int, samples: list<string>}
 */
function workermanKeepAliveRounds(int $port, int $clients, int $rounds): array
{
    $url = "http://127.0.0.1:$port/_internal/gateway/plan";
    $sockets = array_fill(0, $clients, null);
    $result = ['ok' => 0, 'empty' => 0, 'other' => 0, 'samples' => []];

    for ($round = 0; $round < $rounds; $round++) {
        foreach ($sockets as $client => $socket) {
            if ($socket !== null && gatewayPlannerPeerClosed($socket)) {
                fclose($socket);
                $socket = null;
            }
            $socket ??= gatewayPlannerConnect('127.0.0.1', $port, 20.0);
            gatewayPlannerSendRequest($socket, 'POST', $url, workermanPlanHeaders(), workermanPlanBody(), true);
            $sockets[$client] = $socket;
        }

        foreach ($sockets as $client => $socket) {
            $response = gatewayPlannerReadResponse($socket, false);
            if (! $response['complete']) {
                $result['empty']++;
                $result['samples'][] = "round $round client $client: connection closed without a complete reply";
                fclose($socket);
                $sockets[$client] = null;

                continue;
            }
            if ($response['status'] !== 200) {
                $result['other']++;
                $result['samples'][] = "round $round client $client: HTTP {$response['status']}";
            } else {
                $result['ok']++;
            }
            if (strtolower($response['headers']['connection'] ?? '') === 'close') {
                fclose($socket);
                $sockets[$client] = null;
            }
        }
    }

    foreach ($sockets as $socket) {
        if ($socket !== null) {
            fclose($socket);
        }
    }

    return $result;
}

it('serves every keep-alive request when no worker recycles (control)', function (): void {
    workermanScenario([
        'GATEWAY_PLANNER_WORKERS' => '2',
        'GATEWAY_PLANNER_MAX_REQUESTS' => '1000000',
    ], function (GatewayPlannerHostHandle $handle, int $port): void {
        $result = workermanKeepAliveRounds($port, 8, 30);

        expect($result['samples'])->toBe([])
            ->and($result['ok'])->toBe(240);
    });
});

it('serves every keep-alive request while workers keep recycling', function (): void {
    // 8 clients over 2 workers and a threshold of 12 requests per worker: each worker recycles
    // about ten times. The old behaviour closed every connection a worker held the moment it
    // recycled, including connections whose next request was already sent.
    workermanScenario([
        'GATEWAY_PLANNER_WORKERS' => '2',
        'GATEWAY_PLANNER_MAX_REQUESTS' => '12',
    ], function (GatewayPlannerHostHandle $handle, int $port): void {
        $result = workermanKeepAliveRounds($port, 8, 30);

        expect($result['samples'])->toBe([])
            ->and($result['ok'])->toBe(240);
    });
});

it('answers a request already pending on another connection when its worker recycles', function (): void {
    // One worker, recycling on its third request. While the third request is held in the
    // handler, a second client sends its next request: the worker has accepted that connection
    // but cannot read the request yet. When the worker recycles, that request must still be
    // answered rather than dropped with the connection.
    workermanScenario(
        fn (string $directory): array => [
            'GATEWAY_PLANNER_WORKERS' => '1',
            'GATEWAY_PLANNER_MAX_REQUESTS' => '3',
            'GATEWAY_TEST_HOLD_ENTERED_FILE' => $directory.'/hold-entered',
            'GATEWAY_TEST_HOLD_RELEASE_FILE' => $directory.'/hold-release',
        ],
        function (GatewayPlannerHostHandle $handle, int $port, string $directory): void {
            $url = "http://127.0.0.1:$port/_internal/gateway/plan";

            $idle = gatewayPlannerOpenRequest('POST', $url, workermanPlanHeaders(), workermanPlanBody(), 10.0, true);
            workermanExpectRelayPlan(gatewayPlannerReadResponse($idle, false));
            $waiting = gatewayPlannerOpenRequest('POST', $url, workermanPlanHeaders(), workermanPlanBody(), 10.0, true);
            workermanExpectRelayPlan(gatewayPlannerReadResponse($waiting, false));

            $last = gatewayPlannerOpenRequest('POST', $url, workermanPlanHeaders(), workermanPlanBody('/http-smoke/hold'), 20.0, true);
            $deadline = microtime(true) + 15.0;
            while (! is_file($directory.'/hold-entered') && microtime(true) < $deadline) {
                usleep(10_000);
            }
            $this->assertTrue(is_file($directory.'/hold-entered'), 'the third request never entered the handler');

            gatewayPlannerSendRequest($waiting, 'POST', $url, workermanPlanHeaders(), workermanPlanBody(), true);
            usleep(200_000);
            touch($directory.'/hold-release');

            // The held request is the one that reaches the threshold.
            workermanExpectRelayPlan(gatewayPlannerReadResponse($last));

            $pending = gatewayPlannerReadResponse($waiting, false);
            $this->assertTrue($pending['complete'], 'the request pending on the second connection was dropped when the worker recycled');
            workermanExpectRelayPlan($pending);

            // A connection whose client went quiet does not hold the draining worker for ever: the
            // server closes it once the drain window ends, and the replacement worker serves again.
            $deadline = microtime(true) + 8.0;
            while (! gatewayPlannerPeerClosed($idle) && microtime(true) < $deadline) {
                usleep(50_000);
            }
            $this->assertTrue(gatewayPlannerPeerClosed($idle), 'the idle connection was still open after the drain window');
            fclose($waiting);
            fclose($idle);

            $after = null;
            $deadline = microtime(true) + 10.0;
            while (microtime(true) < $deadline) {
                $after = workermanPlan($port);
                if ($after['status'] === 200) {
                    break;
                }
                usleep(50_000);
            }
            workermanExpectRelayPlan($after);
        },
    );
});

it('exits on SIGQUIT while a recycled worker is still draining an idle connection', function (): void {
    workermanScenario([
        'GATEWAY_PLANNER_WORKERS' => '1',
        'GATEWAY_PLANNER_MAX_REQUESTS' => '2',
    ], function (GatewayPlannerHostHandle $handle, int $port): void {
        $url = "http://127.0.0.1:$port/_internal/gateway/plan";

        // The first client keeps its connection open and quiet. The second request reaches the
        // threshold, so the worker starts draining and cannot finish while the first is open.
        $idle = gatewayPlannerOpenRequest('POST', $url, workermanPlanHeaders(), workermanPlanBody(), 10.0, true);
        workermanExpectRelayPlan(gatewayPlannerReadResponse($idle, false));
        $last = gatewayPlannerOpenRequest('POST', $url, workermanPlanHeaders(), workermanPlanBody(), 10.0, true);
        $reply = gatewayPlannerReadResponse($last);
        workermanExpectRelayPlan($reply);
        expect($reply['headers']['connection'] ?? null)->toBe('close');
        $this->assertFalse(gatewayPlannerPeerClosed($idle), 'the draining worker closed the idle connection at once');

        expect(gatewayPlannerSignalHost($handle, SIGQUIT))->toBeTrue();
        $exited = gatewayPlannerWaitForExit($handle, 10.0);
        fclose($idle);

        $this->assertTrue($exited, 'the host did not exit on SIGQUIT while a recycled worker was draining');
        expect($handle->exit)->toBe(['exitCode' => 0, 'signaled' => false, 'termsig' => 0]);
    });
});

it('drains an in-flight request on SIGQUIT and exits cleanly without being forced', function (): void {
    gatewayPlannerWithDisposableDirectory(function (string $dataDirectory): void {
        $database = $dataDirectory.'/gateway.sqlite';
        touch($database);
        $entered = $dataDirectory.'/hold-entered';
        $release = $dataDirectory.'/hold-release';
        $baseEnv = gatewayPlannerBaseEnv($dataDirectory, $database);

        gatewayPlannerWithEnvironment(
            array_merge($baseEnv, [
                'GATEWAY_PLANNER_WORKERS' => '1',
                'GATEWAY_TEST_HOLD_ENTERED_FILE' => $entered,
                'GATEWAY_TEST_HOLD_RELEASE_FILE' => $release,
            ]),
            function () use ($dataDirectory, $database, $baseEnv, $entered, $release): void {
                gatewayPlannerRunMigrations($database);
                gatewayPlannerSeedEndpoint($baseEnv, WORKERMAN_ENDPOINT_KEY, 'application', 'app-123', WORKERMAN_RELAY_URL, WORKERMAN_SUBSCRIBER_ID);

                $port = gatewayPlannerFindFreePort();
                $handle = gatewayPlannerStartWorkerman($dataDirectory, $port);
                try {
                    gatewayPlannerWaitForPort($port);

                    // A request that enters the handler and stays there until released.
                    $socket = gatewayPlannerOpenRequest(
                        'POST',
                        "http://127.0.0.1:$port/_internal/gateway/plan",
                        workermanPlanHeaders(),
                        workermanPlanBody('/http-smoke/hold'),
                        30.0,
                    );
                    $deadline = microtime(true) + 15.0;
                    while (! is_file($entered) && microtime(true) < $deadline) {
                        usleep(10_000);
                    }
                    $this->assertTrue(is_file($entered), 'the request never entered the handler');
                    expect(gatewayPlannerWorkerPids($handle->masterPid))->toContain((int) file_get_contents($entered));

                    // Send the graceful signal while the request is active. Hold it
                    // open for longer than Workerman's 2 second stop timeout, which
                    // is when a non-graceful stop would have SIGKILLed the worker.
                    expect(gatewayPlannerSignalHost($handle, SIGQUIT))->toBeTrue();
                    usleep(3_000_000);

                    $this->assertTrue(gatewayPlannerPollHost($handle), 'the master exited while a request was still in flight');
                    $this->assertFalse(gatewayPlannerSocketReadable($socket, 0.2), 'the in-flight connection was closed or answered early');

                    // Release it: the complete response must still be delivered.
                    touch($release);
                    $response = gatewayPlannerReadResponse($socket);
                    $this->assertTrue($response['complete'], 'the in-flight response was truncated');
                    workermanExpectRelayPlan($response);

                    // The master then exits by itself, cleanly, within a bound.
                    $this->assertTrue(gatewayPlannerWaitForExit($handle, 15.0), 'the master did not exit after draining');
                    expect($handle->exit)->toBe(['exitCode' => 0, 'signaled' => false, 'termsig' => 0]);

                    $stop = gatewayPlannerStopWorkerman($handle);
                    expect($stop['forced'])->toBeFalse()->and($stop['signal'])->toBeNull();

                    // And it no longer accepts connections.
                    $refused = false;
                    for ($attempt = 0; $attempt < 20 && ! $refused; $attempt++) {
                        try {
                            gatewayPlannerRequest('GET', "http://127.0.0.1:$port/health", [], '', 0.5);
                            usleep(50_000);
                        } catch (Throwable) {
                            $refused = true;
                        }
                    }
                    expect($refused)->toBeTrue();
                } finally {
                    gatewayPlannerStopWorkerman($handle);
                }
            },
        );
    });
});

it('exits on SIGQUIT even while clients keep idle keep-alive connections open', function (): void {
    // Workerman's graceful stop waits for every connection to close and never closes one itself,
    // so a client that keeps its connection pool open (a proxy, an HTTP client with keep-alive)
    // would otherwise hold the worker, the master and the container until a forced kill.
    workermanScenario(['GATEWAY_PLANNER_WORKERS' => '2'], function (GatewayPlannerHostHandle $handle, int $port): void {
        $idle = [];
        for ($client = 0; $client < 6; $client++) {
            $socket = gatewayPlannerOpenRequest('POST', "http://127.0.0.1:$port/_internal/gateway/plan", workermanPlanHeaders(), workermanPlanBody());
            workermanExpectRelayPlan(gatewayPlannerReadResponse($socket, false));
            $idle[] = $socket; // the response is complete, but the client keeps the connection open
        }

        expect(gatewayPlannerSignalHost($handle, SIGQUIT))->toBeTrue();
        $exited = gatewayPlannerWaitForExit($handle, 10.0);
        foreach ($idle as $socket) {
            fclose($socket);
        }

        $this->assertTrue($exited, 'the host did not exit on SIGQUIT while clients held idle keep-alive connections');
        expect($handle->exit)->toBe(['exitCode' => 0, 'signaled' => false, 'termsig' => 0]);
    });
});

it('exits on SIGQUIT after a request made the application throw', function (): void {
    // A request that throws is logged with its exception, and the exception's trace arguments hold
    // the request and so its connection. Nothing in the production host keeps the exception, so the
    // connection object is freed and a graceful stop (which waits for every connection object to be
    // freed) completes. The host under test must therefore run as production: under APP_ENV=testing
    // Laravel keeps every logged exception for the life of the process and the stop never completes.
    // See gatewayPlannerStartWorkerman().
    workermanScenario(['GATEWAY_PLANNER_WORKERS' => '1', 'GATEWAY_PLANNER_MAX_REQUESTS' => '1000'], function (GatewayPlannerHostHandle $handle, int $port, string $dataDirectory): void {
        workermanExpectRelayPlan(workermanPlan($port, '/http-smoke/probe/environment'));
        $probe = gatewayPlannerReadProbe($dataDirectory, 'environment');
        expect($probe['environment'])->toBe('production')
            ->and($probe['running_unit_tests'])->toBeFalse();

        $failed = workermanPlan($port, '/http-smoke/write-then-throw/stop-after-throw');
        expect($failed['status'])->toBe(500);
        // The same worker keeps serving after the failure.
        workermanExpectRelayPlan(workermanPlan($port));

        expect(gatewayPlannerSignalHost($handle, SIGQUIT))->toBeTrue();
        $exited = gatewayPlannerWaitForExit($handle, 10.0);

        $this->assertTrue($exited, 'the host did not exit on SIGQUIT after a request that threw');
        expect($handle->exit)->toBe(['exitCode' => 0, 'signaled' => false, 'termsig' => 0]);
    });
});

it('stops an owned host idempotently and never signals a saved PID once the handle is closed', function (): void {
    // An independent process stands in for "an unrelated process that now owns
    // the PID we saved".
    $bystander = proc_open(['sleep', '60'], [0 => ['file', '/dev/null', 'r'], 1 => ['file', '/dev/null', 'w'], 2 => ['file', '/dev/null', 'w']], $pipes);
    expect($bystander)->toBeResource();
    $bystanderPid = (int) proc_get_status($bystander)['pid'];

    try {
        workermanScenario([], function (GatewayPlannerHostHandle $handle, int $port): void {
            expect(gatewayPlannerRequest('GET', "http://127.0.0.1:$port/health")['status'])->toBe(204);

            $first = gatewayPlannerStopWorkerman($handle);
            expect($first['forced'])->toBeFalse()->and($first['signal'])->toBe('SIGQUIT');
            expect($handle->closed)->toBeTrue();
            // Reaped, not left as a zombie.
            expect(gatewayPlannerProcessAlive($handle->masterPid))->toBeFalse();

            // A second stop (the scenario's own finally block makes a third) is a
            // no-op that returns the same recorded result.
            expect(gatewayPlannerStopWorkerman($handle))->toBe($first);
            expect(gatewayPlannerSignalHost($handle, SIGKILL))->toBeFalse();
        });

        // A closed handle must not signal its saved PID even when that PID now
        // belongs to another process.
        $stale = new GatewayPlannerHostHandle(null, $bystanderPid, '/dev/null');
        $stale->closed = true;
        expect(gatewayPlannerStopWorkerman($stale))->toBe(['forced' => false, 'signal' => null, 'exit' => null]);
        expect(gatewayPlannerSignalHost($stale, SIGKILL))->toBeFalse();
        expect(gatewayPlannerProcessAlive($bystanderPid))->toBeTrue();
    } finally {
        @proc_terminate($bystander, SIGKILL);
        @proc_close($bystander);
    }
});

it('leaves an independently started host running after owned shutdown', function (): void {
    // Start an independent host directly (not part of the scenario under test).
    gatewayPlannerWithDisposableDirectory(function (string $independentDataDirectory): void {
        $independentDatabase = $independentDataDirectory.'/gateway.sqlite';
        touch($independentDatabase);
        $independentPort = gatewayPlannerFindFreePort();

        gatewayPlannerWithEnvironment(
            gatewayPlannerBaseEnv($independentDataDirectory, $independentDatabase),
            function () use ($independentDataDirectory, $independentDatabase, $independentPort): void {
                gatewayPlannerRunMigrations($independentDatabase);
                $independent = gatewayPlannerStartWorkerman($independentDataDirectory, $independentPort);

                try {
                    gatewayPlannerWaitForPort($independentPort);
                    expect(gatewayPlannerRequest('GET', "http://127.0.0.1:$independentPort/health")['status'])->toBe(204);

                    // Run an owned host and shut it down; the independent host must survive.
                    workermanScenario([], function (GatewayPlannerHostHandle $handle, int $port): void {
                        expect(gatewayPlannerRequest('GET', "http://127.0.0.1:$port/health")['status'])->toBe(204);
                    });

                    expect(gatewayPlannerPollHost($independent))->toBeTrue();
                    expect(gatewayPlannerRequest('GET', "http://127.0.0.1:$independentPort/health")['status'])->toBe(204);
                } finally {
                    gatewayPlannerStopWorkerman($independent);
                }
            },
        );
    });
});

it('runs migrations only against a disposable database', function (): void {
    $database = tempnam(sys_get_temp_dir(), 'gateway-disposable-schema-');
    try {
        $result = GatewayMigrationRunner::sqlite(
            database: $database,
            migrationsPath: gatewayPlannerRepositoryRoot().'/pkg/planner/migrations',
            tables: [],
        )->migrate();
        expect($result->applied)->not->toBeEmpty();

        $pdo = new PDO('sqlite:'.$database);
        $tables = $pdo->query("SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name")
            ->fetchAll(PDO::FETCH_COLUMN);
        expect($tables)->toContain('gateway_migrations', 'configurations', 'targets');
    } finally {
        if (is_string($database) && file_exists($database)) {
            unlink($database);
        }
    }
});

// ---------------------------------------------------------------------------
// Database protection: a sentinel database must survive conflicting settings
// ---------------------------------------------------------------------------

/**
 * Everything a test can observe about a SQLite database without changing it:
 * its schema, its row counts, and the bytes of the file.
 *
 * @return array{schema: list<array<string, mixed>>, counts: array<string, int>, sha1: string}
 */
function sentinelSqliteFingerprint(string $file): array
{
    $pdo = new PDO('sqlite:'.$file);
    $schema = $pdo->query("SELECT type, name, sql FROM sqlite_master ORDER BY type, name")->fetchAll(PDO::FETCH_ASSOC);
    $counts = [];
    foreach ($pdo->query("SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'")->fetchAll(PDO::FETCH_COLUMN) as $table) {
        $counts[$table] = (int) $pdo->query('SELECT COUNT(*) FROM "'.$table.'"')->fetchColumn();
    }
    $pdo = null;

    return ['schema' => $schema, 'counts' => $counts, 'sha1' => sha1_file($file)];
}

/**
 * Build a Laravel config cache that bakes the sentinel database in, the way an
 * inherited APP_CONFIG_CACHE from another deployment would.
 */
function sentinelHostileConfigCache(string $directory, string $sentinel): string
{
    $cache = $directory.'/hostile-config.php';
    gatewayPlannerWithEnvironment([
        'APP_CONFIG_CACHE' => $cache,
        'DB_CONNECTION' => 'sqlite',
        'DB_DATABASE' => $sentinel,
        'GATEWAY_ROUTER_DATA_DIR' => $directory,
        'LARAVEL_STORAGE_PATH' => $directory.'/storage',
        'APP_KEY' => 'base64:'.base64_encode(random_bytes(32)),
    ], function () use ($directory): void {
        @mkdir($directory.'/storage/framework/cache/data', 0700, true);
        @mkdir($directory.'/storage/framework/views', 0700, true);
        @mkdir($directory.'/storage/logs', 0700, true);
        exec('php '.escapeshellarg(gatewayPlannerRepositoryRoot().'/app/planner/artisan').' config:cache --no-ansi 2>&1', $output, $exitCode);
        if ($exitCode !== 0) {
            throw new RuntimeException("Unable to build the hostile config cache:\n".implode("\n", $output));
        }
    });

    return $cache;
}

it('leaves a sentinel SQLite database untouched when inherited settings point at it', function (): void {
    gatewayPlannerWithDisposableDirectory(function (string $sentinelDirectory): void {
        $sentinel = $sentinelDirectory.'/sentinel.sqlite';
        $pdo = new PDO('sqlite:'.$sentinel);
        $pdo->exec('CREATE TABLE sentinel_marker (id INTEGER PRIMARY KEY, note TEXT)');
        $pdo->exec("INSERT INTO sentinel_marker (note) VALUES ('do not touch')");
        $pdo = null;
        $before = sentinelSqliteFingerprint($sentinel);

        $hostileCache = sentinelHostileConfigCache($sentinelDirectory, $sentinel);
        $garbage = $sentinelDirectory.'/garbage-cache.php';
        file_put_contents($garbage, "<?php return 'not a cache';\n");
        $hostile = [
            'APP_CONFIG_CACHE' => $hostileCache,
            'APP_ROUTES_CACHE' => $garbage,
            'APP_SERVICES_CACHE' => $garbage,
            'APP_PACKAGES_CACHE' => $garbage,
            'APP_EVENTS_CACHE' => $garbage,
            'DB_URL' => 'sqlite:///'.$sentinel,
            'DATABASE_URL' => 'sqlite:///'.$sentinel,
        ];

        // Positive control: these inherited settings really do redirect Laravel to
        // the sentinel when nothing neutralises them.
        gatewayPlannerWithEnvironment(array_merge($hostile, [
            'DB_CONNECTION' => 'sqlite',
            'GATEWAY_ROUTER_DATA_DIR' => $sentinelDirectory,
            'LARAVEL_STORAGE_PATH' => $sentinelDirectory.'/storage',
            'APP_ROUTES_CACHE' => null,
            'APP_SERVICES_CACHE' => null,
            'APP_PACKAGES_CACHE' => null,
            'APP_EVENTS_CACHE' => null,
        ]), function () use ($sentinel): void {
            $resolved = gatewayPlannerResolveDatabase();
            expect($resolved['config_cached'])->toBeTrue()
                ->and(realpath((string) $resolved['file']))->toBe(realpath($sentinel));
        });

        // With the conflicting settings inherited, a normal scenario must migrate,
        // seed and serve from its own database and leave the sentinel alone.
        gatewayPlannerWithEnvironment($hostile, function (): void {
            workermanScenario([], function (GatewayPlannerHostHandle $handle, int $port, string $dataDirectory): void {
                workermanExpectRelayPlan(workermanPlan($port));
                $own = new PDO('sqlite:'.$dataDirectory.'/gateway.sqlite');
                expect((int) $own->query("SELECT COUNT(*) FROM sqlite_master WHERE name = 'configurations'")->fetchColumn())->toBe(1);
            });
        });

        // If isolation were ever defeated, the resolved-database check must refuse
        // before anything is written.
        $defeated = fn () => gatewayPlannerWithDisposableDirectory(function (string $dataDirectory) use ($hostileCache): void {
            $database = $dataDirectory.'/gateway.sqlite';
            touch($database);
            gatewayPlannerWithEnvironment(
                array_merge(gatewayPlannerBaseEnv($dataDirectory, $database), ['APP_CONFIG_CACHE' => $hostileCache]),
                function () use ($database): void {
                    gatewayPlannerRunMigrations($database);
                },
            );
        });
        expect($defeated)->toThrow(RuntimeException::class, 'Refusing to write');

        expect(sentinelSqliteFingerprint($sentinel))->toBe($before);
    });
});

/** @return array{tables: list<string>, counts: array<string, int>} */
function sentinelPostgresFingerprint(array $connection, string $database): array
{
    $pdo = new PDO(
        sprintf('pgsql:host=%s;port=%d;dbname=%s', $connection['host'], $connection['port'], $database),
        $connection['username'],
        $connection['password'],
    );
    $tables = $pdo->query("SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' ORDER BY table_name")
        ->fetchAll(PDO::FETCH_COLUMN);
    $counts = [];
    foreach ($tables as $table) {
        $counts[$table] = (int) $pdo->query('SELECT COUNT(*) FROM "'.$table.'"')->fetchColumn();
    }

    return ['tables' => $tables, 'counts' => $counts];
}

/**
 * Provision a randomly named database on the configured PostgreSQL server and
 * hand its connection details to the body, dropping it afterwards.
 *
 * @param array{host: string, port: int, username: string, password: string} $server
 * @param callable(array{host: string, port: int, database: string, username: string, password: string}): mixed $body
 */
function withDisposablePostgresDatabase(array $server, string $prefix, callable $body): mixed
{
    $name = $prefix.bin2hex(random_bytes(8));
    $admin = fn (): PDO => new PDO(
        sprintf('pgsql:host=%s;port=%d;dbname=postgres', $server['host'], $server['port']),
        $server['username'],
        $server['password'],
    );
    $admin()->exec('CREATE DATABASE "'.$name.'"');

    try {
        return $body([
            'host' => $server['host'],
            'port' => $server['port'],
            'database' => $name,
            'username' => $server['username'],
            'password' => $server['password'],
        ]);
    } finally {
        try {
            $admin()->exec('DROP DATABASE IF EXISTS "'.$name.'" WITH (FORCE)');
        } catch (Throwable) {
            // Best-effort cleanup of the disposable database.
        }
    }
}

it('leaves a sentinel PostgreSQL database untouched when DB_URL points at it', function (): void {
    $server = gatewayPlannerPostgresConnection();
    if ($server === null) {
        $this->markTestSkipped('Set GATEWAY_TEST_POSTGRES_DATABASE to run the PostgreSQL sentinel test.');
    }

    withDisposablePostgresDatabase($server, 'gateway_sentinel_', function (array $sentinel) use ($server): void {
        $pdo = new PDO(
            sprintf('pgsql:host=%s;port=%d;dbname=%s', $sentinel['host'], $sentinel['port'], $sentinel['database']),
            $sentinel['username'],
            $sentinel['password'],
        );
        $pdo->exec('CREATE TABLE sentinel_marker (id serial PRIMARY KEY, note text)');
        $pdo->exec("INSERT INTO sentinel_marker (note) VALUES ('do not touch')");
        $pdo = null;
        $before = sentinelPostgresFingerprint($server, $sentinel['database']);

        $url = sprintf(
            'postgresql://%s:%s@%s:%d/%s',
            rawurlencode($sentinel['username']),
            rawurlencode($sentinel['password']),
            $sentinel['host'],
            $sentinel['port'],
            $sentinel['database'],
        );
        $hostile = ['DB_URL' => $url, 'DATABASE_URL' => $url];

        // Positive control: DB_URL really redirects the resolved database to the
        // sentinel when nothing neutralises it.
        gatewayPlannerWithDisposableDirectory(function (string $directory) use ($hostile, $sentinel): void {
            gatewayPlannerWithEnvironment(array_merge($hostile, [
                'DB_CONNECTION' => 'pgsql',
                'GATEWAY_ROUTER_DATA_DIR' => $directory,
                'LARAVEL_STORAGE_PATH' => $directory.'/storage',
                'APP_KEY' => 'base64:'.base64_encode(random_bytes(32)),
            ]), function () use ($sentinel): void {
                expect(gatewayPlannerResolveDatabase()['database'])->toBe($sentinel['database']);
            });
        });

        // With DB_URL inherited, a disposable PostgreSQL scenario must still use
        // its own database: migrate, seed, serve, and leave the sentinel alone.
        withDisposablePostgresDatabase($server, 'gateway_sentinel_run_', function (array $own) use ($hostile, $server): void {
            gatewayPlannerWithEnvironment($hostile, function () use ($own): void {
                gatewayPlannerWithDisposableDirectory(function (string $dataDirectory) use ($own): void {
                    $baseEnv = gatewayPlannerBaseEnvPostgres($dataDirectory, $own);
                    gatewayPlannerWithEnvironment($baseEnv, function () use ($dataDirectory, $own, $baseEnv): void {
                        gatewayPlannerRunMigrationsPostgres($own);
                        gatewayPlannerSeedEndpoint($baseEnv, WORKERMAN_ENDPOINT_KEY, 'application', 'app-123', WORKERMAN_RELAY_URL, WORKERMAN_SUBSCRIBER_ID);

                        $port = gatewayPlannerFindFreePort();
                        $handle = gatewayPlannerStartWorkerman($dataDirectory, $port);
                        try {
                            gatewayPlannerWaitForPort($port);
                            workermanExpectRelayPlan(workermanPlan($port));
                        } finally {
                            gatewayPlannerStopWorkerman($handle);
                        }
                    });
                });
            });

            expect(sentinelPostgresFingerprint($server, $own['database'])['tables'])->toContain('gateway_migrations', 'configurations');
        });

        expect(sentinelPostgresFingerprint($server, $sentinel['database']))->toBe($before);
    });
});

// ---------------------------------------------------------------------------
// Cleanup on failure
// ---------------------------------------------------------------------------

it('restores the environment and removes the data directory when setup fails', function (): void {
    $before = getenv();
    $captured = null;

    $failing = function () use (&$captured): void {
        gatewayPlannerWithDisposableDirectory(function (string $dataDirectory) use (&$captured): void {
            $captured = $dataDirectory;
            $database = $dataDirectory.'/gateway.sqlite';
            $other = $dataDirectory.'/other.sqlite';
            touch($database);
            touch($other);

            gatewayPlannerWithEnvironment(gatewayPlannerBaseEnv($dataDirectory, $database), function () use ($other): void {
                // Asks to migrate a database the environment does not select.
                gatewayPlannerRunMigrations($other);
            });
        });
    };

    expect($failing)->toThrow(RuntimeException::class, 'Refusing to migrate');
    expect($captured)->toBeString()->and(is_dir($captured))->toBeFalse();
    expect(getenv())->toBe($before);
});

it('stops the host and removes the data directory when the test body fails', function (): void {
    $before = getenv();
    $captured = null;

    $failing = function () use (&$captured): void {
        workermanScenario([], function (GatewayPlannerHostHandle $handle, int $port, string $dataDirectory) use (&$captured): void {
            $captured = [$handle, $dataDirectory];
            expect(gatewayPlannerRequest('GET', "http://127.0.0.1:$port/health")['status'])->toBe(204);

            throw new LogicException('simulated assertion failure');
        });
    };

    expect($failing)->toThrow(LogicException::class, 'simulated assertion failure');
    [$handle, $dataDirectory] = $captured;
    expect($handle->closed)->toBeTrue()
        ->and(gatewayPlannerProcessAlive($handle->masterPid))->toBeFalse()
        ->and(is_dir($dataDirectory))->toBeFalse();
    expect(getenv())->toBe($before);
});

it('stops the host and removes the data directory when readiness fails after startup', function (): void {
    $before = getenv();
    $captured = null;

    $failing = function () use (&$captured): void {
        gatewayPlannerWithDisposableDirectory(function (string $dataDirectory) use (&$captured): void {
            $database = $dataDirectory.'/gateway.sqlite';
            touch($database);
            gatewayPlannerWithEnvironment(gatewayPlannerBaseEnv($dataDirectory, $database), function () use ($dataDirectory, $database, &$captured): void {
                gatewayPlannerRunMigrations($database);
                $port = gatewayPlannerFindFreePort();
                $handle = gatewayPlannerStartWorkerman($dataDirectory, $port);
                $captured = [$handle, $dataDirectory];
                try {
                    // Wait on a port nothing listens on, so readiness times out.
                    gatewayPlannerWaitForPort(gatewayPlannerFindFreePort(), 0.4);
                } finally {
                    gatewayPlannerStopWorkerman($handle);
                }
            });
        });
    };

    expect($failing)->toThrow(RuntimeException::class, 'did not accept connections');
    [$handle, $dataDirectory] = $captured;
    expect($handle->closed)->toBeTrue()
        ->and(gatewayPlannerProcessAlive($handle->masterPid))->toBeFalse()
        ->and(is_dir($dataDirectory))->toBeFalse();
    expect(getenv())->toBe($before);
});

// ---------------------------------------------------------------------------
// PostgreSQL reconnection
// ---------------------------------------------------------------------------

it('reconnects to PostgreSQL on the same worker after its backend is terminated', function (): void {
    $server = gatewayPlannerPostgresConnection();
    if ($server === null) {
        $this->markTestSkipped('Set GATEWAY_TEST_POSTGRES_DATABASE to run the PostgreSQL reconnection test.');
    }

    // A truly disposable database so the test never touches an existing
    // application or shared test database.
    withDisposablePostgresDatabase($server, 'gateway_disconnect_', function (array $disposable) use ($server): void {
        gatewayPlannerWithDisposableDirectory(function (string $dataDirectory) use ($disposable, $server): void {
            $baseEnv = gatewayPlannerBaseEnvPostgres($dataDirectory, $disposable);

            gatewayPlannerWithEnvironment(
                array_merge($baseEnv, ['GATEWAY_PLANNER_WORKERS' => '1', 'GATEWAY_PLANNER_MAX_REQUESTS' => '1000']),
                function () use ($dataDirectory, $disposable, $server, $baseEnv): void {
                    gatewayPlannerRunMigrationsPostgres($disposable);
                    gatewayPlannerSeedEndpoint($baseEnv, WORKERMAN_ENDPOINT_KEY, 'application', 'app-123', WORKERMAN_RELAY_URL, WORKERMAN_SUBSCRIBER_ID);

                    $admin = new PDO(
                        sprintf('pgsql:host=%s;port=%d;dbname=postgres', $server['host'], $server['port']),
                        $server['username'],
                        $server['password'],
                    );

                    $port = gatewayPlannerFindFreePort();
                    $handle = gatewayPlannerStartWorkerman($dataDirectory, $port);
                    try {
                        gatewayPlannerWaitForPort($port);

                        // Before: the worker serves a plan and reports the PostgreSQL
                        // backend its own connection is bound to.
                        workermanExpectRelayPlan(workermanPlan($port, '/http-smoke/probe/before'));
                        $before = gatewayPlannerReadProbe($dataDirectory, 'before');
                        expect($before['worker_pid'])->toBeInt()->and($before['backend_pid'])->toBeInt();
                        expect(gatewayPlannerWorkerPids($handle->masterPid))->toContain($before['worker_pid']);

                        // Terminate exactly that backend from a separate connection,
                        // and require the server to confirm it did.
                        $terminated = $admin->query('SELECT pg_terminate_backend('.(int) $before['backend_pid'].')')->fetchColumn();
                        $this->assertTrue(in_array($terminated, [true, 't', 1, '1'], true), 'pg_terminate_backend did not confirm termination');

                        // Wait until the backend is really gone, so the next request
                        // cannot race the termination and run on the old session.
                        $deadline = microtime(true) + 10.0;
                        do {
                            $alive = (int) $admin->query('SELECT COUNT(*) FROM pg_stat_activity WHERE pid = '.(int) $before['backend_pid'])->fetchColumn();
                            if ($alive === 0) {
                                break;
                            }
                            usleep(25_000);
                        } while (microtime(true) < $deadline);
                        expect($alive)->toBe(0);

                        // The first request after the drop: no retry loop, so a
                        // user-visible error cannot be hidden. The same Workerman
                        // child performs an uncached read on a new backend.
                        $firstAfterDrop = workermanPlan($port, '/http-smoke/probe/after');
                        $this->assertSame(200, $firstAfterDrop['status'], 'the first request after the disconnect failed: '.$firstAfterDrop['body']);
                        workermanExpectRelayPlan($firstAfterDrop);

                        $after = gatewayPlannerReadProbe($dataDirectory, 'after');
                        expect($after['worker_pid'])->toBe($before['worker_pid'])
                            ->and($after['backend_pid'])->toBeInt()->not->toBe($before['backend_pid']);
                        expect((int) $admin->query('SELECT COUNT(*) FROM pg_stat_activity WHERE pid = '.(int) $after['backend_pid'])->fetchColumn())->toBe(1);
                        expect(gatewayPlannerWorkerPids($handle->masterPid))->toContain($after['worker_pid']);
                    } finally {
                        $stop = gatewayPlannerStopWorkerman($handle);
                    }
                    expect($stop['forced'])->toBeFalse();
                },
            );
        });
    });
});

// ---------------------------------------------------------------------------
// MySQL (the Planner's MySQL 8.4 runtime; see docs/deployment.md#database-support)
// ---------------------------------------------------------------------------

/** @return array{tables: list<string>, counts: array<string, int>} */
function sentinelMysqlFingerprint(array $connection, string $database): array
{
    $pdo = new PDO(
        sprintf('mysql:host=%s;port=%d;dbname=%s', $connection['host'], $connection['port'], $database),
        $connection['username'],
        $connection['password'],
    );
    $tables = $pdo->query('SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() ORDER BY table_name')
        ->fetchAll(PDO::FETCH_COLUMN);
    $counts = [];
    foreach ($tables as $table) {
        $counts[$table] = (int) $pdo->query('SELECT COUNT(*) FROM `'.$table.'`')->fetchColumn();
    }

    return ['tables' => $tables, 'counts' => $counts];
}

/**
 * Create a disposable MySQL database next to the configured test one, with the
 * server's own default collation, run the body with its connection, and drop it.
 *
 * @param array{host: string, port: int, database: string, username: string, password: string} $server
 */
function withDisposableMysqlDatabase(array $server, string $prefix, callable $body): mixed
{
    $name = $prefix.bin2hex(random_bytes(8));
    $admin = fn (): PDO => new PDO(
        sprintf('mysql:host=%s;port=%d;dbname=%s', $server['host'], $server['port'], $server['database']),
        $server['username'],
        $server['password'],
    );
    $admin()->exec('CREATE DATABASE `'.$name.'`');

    try {
        return $body([
            'host' => $server['host'],
            'port' => $server['port'],
            'database' => $name,
            'username' => $server['username'],
            'password' => $server['password'],
        ]);
    } finally {
        try {
            $admin()->exec('DROP DATABASE IF EXISTS `'.$name.'`');
        } catch (Throwable) {
            // Best-effort cleanup of the disposable database.
        }
    }
}

/**
 * Like workermanScenario(), against a disposable MySQL database: migrated the way
 * scripts/planner-start migrates, seeded, served by a real Workerman process, and
 * stopped through its owned handle.
 *
 * @param array<string, string> $extraEnv
 * @param callable(GatewayPlannerHostHandle, int, string, array<string, mixed>): void $body receives the handle,
 *        port, data directory and the disposable database's connection
 */
function workermanMysqlScenario(array $server, array $extraEnv, callable $body): void
{
    withDisposableMysqlDatabase($server, 'gateway_workerman_', function (array $disposable) use ($extraEnv, $body): void {
        workermanMysqlScenarioIn($disposable, $extraEnv, $body);
    });
}

it('serves planning and management requests from a Workerman host on MySQL', function (): void {
    $server = gatewayPlannerMysqlConnection();
    if ($server === null) {
        $this->markTestSkipped('Set GATEWAY_TEST_MYSQL_DATABASE to run the MySQL Workerman tests.');
    }

    workermanMysqlScenario($server, [], function (GatewayPlannerHostHandle $handle, int $port, string $dataDirectory, array $database): void {
        expect(gatewayPlannerRequest('GET', "http://127.0.0.1:$port/health")['status'])->toBe(204);
        workermanExpectRelayPlan(workermanPlan($port));
        expect(json_decode(workermanPlan($port, '/unbound/path')['body'], true, 512, JSON_THROW_ON_ERROR)['action'])->toBe('pass_through');

        $base = "http://127.0.0.1:$port/targets";
        $authorized = ['Content-Type' => 'application/json', 'Authorization' => 'Bearer '.WORKERMAN_REGISTRY_TOKEN];
        $targetBody = json_encode(['name' => 'workerman-mysql-target', 'driver' => 'local'], JSON_THROW_ON_ERROR);
        expect(gatewayPlannerRequest('POST', $base, ['Content-Type' => 'application/json'], $targetBody)['status'])->toBe(401);
        expect(gatewayPlannerRequest('POST', $base, $authorized, $targetBody)['status'])->toBe(201);
        // Names are exact on MySQL, as on PostgreSQL and SQLite.
        expect(gatewayPlannerRequest('POST', $base, $authorized, json_encode(['name' => 'Workerman-MySQL-Target', 'driver' => 'local']))['status'])->toBe(201);
        expect(gatewayPlannerRequest('GET', $base, ['Authorization' => 'Bearer '.WORKERMAN_REGISTRY_TOKEN])['body'])->toContain('workerman-mysql-target');

        // The rows are in the disposable MySQL database the host resolved.
        $fingerprint = sentinelMysqlFingerprint($database, $database['database']);
        expect($fingerprint['counts']['targets'])->toBe(2)
            ->and($fingerprint['counts']['subscriptions'])->toBeGreaterThan(0);
    });
});

it('leaves a sentinel MySQL database untouched when DB_URL points at it', function (): void {
    $server = gatewayPlannerMysqlConnection();
    if ($server === null) {
        $this->markTestSkipped('Set GATEWAY_TEST_MYSQL_DATABASE to run the MySQL sentinel test.');
    }

    withDisposableMysqlDatabase($server, 'gateway_sentinel_', function (array $sentinel) use ($server): void {
        $pdo = new PDO(
            sprintf('mysql:host=%s;port=%d;dbname=%s', $sentinel['host'], $sentinel['port'], $sentinel['database']),
            $sentinel['username'],
            $sentinel['password'],
        );
        $pdo->exec('CREATE TABLE sentinel_marker (id INT AUTO_INCREMENT PRIMARY KEY, note TEXT)');
        $pdo->exec("INSERT INTO sentinel_marker (note) VALUES ('do not touch')");
        $pdo = null;
        $before = sentinelMysqlFingerprint($sentinel, $sentinel['database']);

        $url = sprintf(
            'mysql://%s:%s@%s:%d/%s',
            rawurlencode($sentinel['username']),
            rawurlencode($sentinel['password']),
            $sentinel['host'],
            $sentinel['port'],
            $sentinel['database'],
        );
        $hostile = ['DB_URL' => $url, 'DATABASE_URL' => $url];

        // Positive control: DB_URL really redirects the resolved database to the
        // sentinel when nothing neutralises it.
        gatewayPlannerWithDisposableDirectory(function (string $directory) use ($hostile, $sentinel): void {
            gatewayPlannerWithEnvironment(array_merge($hostile, [
                'DB_CONNECTION' => 'mysql',
                'GATEWAY_ROUTER_DATA_DIR' => $directory,
                'LARAVEL_STORAGE_PATH' => $directory.'/storage',
                'APP_KEY' => 'base64:'.base64_encode(random_bytes(32)),
            ]), function () use ($sentinel): void {
                expect(gatewayPlannerResolveDatabase()['database'])->toBe($sentinel['database']);
            });
        });

        // With DB_URL inherited, a disposable MySQL scenario must still use its own
        // database: migrate, seed, serve, and leave the sentinel alone.
        withDisposableMysqlDatabase($server, 'gateway_sentinel_run_', function (array $own) use ($hostile, $server): void {
            gatewayPlannerWithEnvironment($hostile, function () use ($own, $server): void {
                workermanMysqlScenarioIn($own, [], function (GatewayPlannerHostHandle $handle, int $port): void {
                    workermanExpectRelayPlan(workermanPlan($port));
                });
            });

            expect(sentinelMysqlFingerprint($server, $own['database'])['tables'])->toContain('gateway_migrations', 'configurations');
        });

        expect(sentinelMysqlFingerprint($server, $sentinel['database']))->toBe($before);
    });
});

/**
 * Migrate, seed and serve a database that already exists (the disposable one
 * workermanMysqlScenario() creates), then run the body and stop the host.
 *
 * @param array{host: string, port: int, database: string, username: string, password: string} $disposable
 * @param array<string, string> $extraEnv
 */
function workermanMysqlScenarioIn(array $disposable, array $extraEnv, callable $body): void
{
    gatewayPlannerWithDisposableDirectory(function (string $dataDirectory) use ($disposable, $extraEnv, $body): void {
        $baseEnv = gatewayPlannerBaseEnvMysql($dataDirectory, $disposable);

        gatewayPlannerWithEnvironment(
            array_merge($baseEnv, $extraEnv),
            function () use ($dataDirectory, $disposable, $baseEnv, $body): void {
                gatewayPlannerRunMigrationsMysql($disposable);
                gatewayPlannerSeedEndpoint($baseEnv, WORKERMAN_ENDPOINT_KEY, 'application', 'app-123', WORKERMAN_RELAY_URL, WORKERMAN_SUBSCRIBER_ID);

                $port = gatewayPlannerFindFreePort();
                $handle = gatewayPlannerStartWorkerman($dataDirectory, $port);
                try {
                    gatewayPlannerWaitForPort($port);
                    $body($handle, $port, $dataDirectory, $disposable);
                } finally {
                    $stop = gatewayPlannerStopWorkerman($handle);
                }

                if ($stop['forced']) {
                    throw new RuntimeException('The host did not exit on SIGQUIT and had to be force-killed.');
                }
            },
        );
    });
}

it('reconnects to MySQL on the same worker after its connection is killed', function (): void {
    $server = gatewayPlannerMysqlConnection();
    if ($server === null) {
        $this->markTestSkipped('Set GATEWAY_TEST_MYSQL_DATABASE to run the MySQL reconnection test.');
    }

    workermanMysqlScenario(
        $server,
        ['GATEWAY_PLANNER_WORKERS' => '1', 'GATEWAY_PLANNER_MAX_REQUESTS' => '1000'],
        function (GatewayPlannerHostHandle $handle, int $port, string $dataDirectory, array $database) use ($server): void {
            $admin = new PDO(
                sprintf('mysql:host=%s;port=%d;dbname=%s', $server['host'], $server['port'], $server['database']),
                $server['username'],
                $server['password'],
            );

            // Before: the worker serves a plan and reports the MySQL thread its own
            // connection is bound to.
            workermanExpectRelayPlan(workermanPlan($port, '/http-smoke/probe/before'));
            $before = gatewayPlannerReadProbe($dataDirectory, 'before');
            expect($before['worker_pid'])->toBeInt()->and($before['backend_pid'])->toBeInt();
            expect(gatewayPlannerWorkerPids($handle->masterPid))->toContain($before['worker_pid']);

            // Kill exactly that thread from a separate connection, and wait until the
            // server no longer lists it, so the next request cannot run on the old session.
            $admin->exec('KILL CONNECTION '.(int) $before['backend_pid']);
            $listed = $admin->prepare('SELECT COUNT(*) FROM information_schema.processlist WHERE id = ?');
            $deadline = microtime(true) + 10.0;
            do {
                $listed->execute([(int) $before['backend_pid']]);
                $alive = (int) $listed->fetchColumn();
                if ($alive === 0) {
                    break;
                }
                usleep(25_000);
            } while (microtime(true) < $deadline);
            expect($alive)->toBe(0);

            // The first request after the kill: no retry loop, so a user-visible error
            // cannot be hidden. The same Workerman child performs an uncached read on a
            // new MySQL thread.
            $firstAfterKill = workermanPlan($port, '/http-smoke/probe/after');
            $this->assertSame(200, $firstAfterKill['status'], 'the first request after the kill failed: '.$firstAfterKill['body']);
            workermanExpectRelayPlan($firstAfterKill);

            $after = gatewayPlannerReadProbe($dataDirectory, 'after');
            expect($after['worker_pid'])->toBe($before['worker_pid'])
                ->and($after['backend_pid'])->toBeInt()->not->toBe($before['backend_pid']);
            $listed->execute([(int) $after['backend_pid']]);
            expect((int) $listed->fetchColumn())->toBe(1);
        },
    );
});

it('rolls back a write left in an open transaction on MySQL and keeps serving from the same worker', function (): void {
    $server = gatewayPlannerMysqlConnection();
    if ($server === null) {
        $this->markTestSkipped('Set GATEWAY_TEST_MYSQL_DATABASE to run the MySQL rollback test.');
    }

    workermanMysqlScenario(
        $server,
        ['GATEWAY_PLANNER_WORKERS' => '1', 'GATEWAY_PLANNER_MAX_REQUESTS' => '1000'],
        function (GatewayPlannerHostHandle $handle, int $port, string $dataDirectory, array $database) use ($server): void {
            $token = 'open-'.bin2hex(random_bytes(4));

            // The request opens a transaction, inserts a row, and ends without committing it.
            workermanExpectRelayPlan(workermanPlan($port, '/http-smoke/write-leave-open/'.$token));
            expect(gatewayPlannerReadProbe($dataDirectory, $token))->toBe(['transaction_level_at_exit' => 1]);

            // The same worker then serves a database-backed request on a clean connection.
            workermanExpectRelayPlan(workermanPlan($port, '/http-smoke/probe/after-open'));
            $after = gatewayPlannerReadProbe($dataDirectory, 'after-open');
            expect($after['transaction_level'])->toBe(0)
                ->and(count(gatewayPlannerWorkerPids($handle->masterPid)))->toBe(1)
                ->and(gatewayPlannerWorkerPids($handle->masterPid))->toContain($after['worker_pid']);

            // The failed write is gone, and MySQL itself holds no open transaction for the
            // worker's thread (an unrolled-back write would still hold its row locks).
            $pdo = new PDO(
                sprintf('mysql:host=%s;port=%d;dbname=%s', $database['host'], $database['port'], $database['database']),
                $database['username'],
                $database['password'],
            );
            $count = $pdo->prepare('SELECT COUNT(*) FROM targets WHERE name = ?');
            $count->execute(['failed-write-'.$token]);
            expect((int) $count->fetchColumn())->toBe(0);

            $open = new PDO(
                sprintf('mysql:host=%s;port=%d;dbname=%s', $server['host'], $server['port'], $server['database']),
                $server['username'],
                $server['password'],
            );
            $transactions = $open->prepare('SELECT COUNT(*) FROM information_schema.innodb_trx WHERE trx_mysql_thread_id = ?');
            $transactions->execute([(int) $after['backend_pid']]);
            expect((int) $transactions->fetchColumn())->toBe(0);
        },
    );
});
