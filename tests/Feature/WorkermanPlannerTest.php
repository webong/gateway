<?php

declare(strict_types=1);

use Webong\Gateway\Migrations\GatewayMigrationRunner;

require_once __DIR__.'/../Support/WorkermanPlannerHelper.php';

/**
 * Real Workerman process integration tests.
 *
 * A genuine `app/workerman.php` process is launched for each scenario against a
 * disposable SQLite database that is migrated and seeded with a real endpoint +
 * relay subscription. The process is exercised over plain HTTP: authenticated
 * planning of a database-backed route, authenticated management mutation +
 * readback, worker recycling with an observed PID change, and graceful
 * shutdown. Nothing here touches an existing application database, and no
 * process other than the one each test launches is terminated.
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

/**
 * Run a full Workerman scenario against a disposable, seeded database.
 *
 * @param array<string, string> $extraEnv additional server variables (workers, max requests).
 * @param callable(array{process: resource, masterPid: int, logFile: string}, int): void $body
 */
function workermanScenario(array $extraEnv, callable $body): void
{
    $dataDirectory = gatewayPlannerDisposableDataDirectory();
    $database = $dataDirectory.'/gateway.sqlite';
    touch($database);

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
                $body($handle, $port);
            } finally {
                gatewayPlannerStopWorkerman($handle);
            }
        },
    );

    gatewayPlannerRemoveDataDirectory($dataDirectory);
}

it('boots a real Workerman Planner host and serves public health', function (): void {
    workermanScenario([], function (array $handle, int $port): void {
        expect(gatewayPlannerRequest('GET', "http://127.0.0.1:$port/health")['status'])->toBe(204);
    });
});

it('enforces authentication on readiness and planning endpoints', function (): void {
    workermanScenario([], function (array $handle, int $port): void {
        expect(gatewayPlannerRequest('GET', "http://127.0.0.1:$port/_internal/gateway/ready")['status'])->toBe(404);
        expect(gatewayPlannerRequest('GET', "http://127.0.0.1:$port/_internal/gateway/ready", ['X-Gateway-Internal' => 'wrong-token'])['status'])->toBe(404);
        expect(gatewayPlannerRequest('GET', "http://127.0.0.1:$port/_internal/gateway/ready", ['X-Gateway-Internal' => WORKERMAN_INTERNAL_TOKEN])['status'])->toBe(204);

        expect(gatewayPlannerRequest('POST', "http://127.0.0.1:$port/_internal/gateway/plan", ['Content-Type' => 'application/json'], workermanPlanBody())['status'])->toBe(404);
        expect(gatewayPlannerRequest('POST', "http://127.0.0.1:$port/_internal/gateway/plan", ['Content-Type' => 'application/json', 'X-Gateway-Internal' => 'wrong-token'], workermanPlanBody())['status'])->toBe(404);
    });
});

it('plans a seeded database-backed route and returns its bound relay', function (): void {
    workermanScenario([], function (array $handle, int $port): void {
        $planned = gatewayPlannerRequest(
            'POST',
            "http://127.0.0.1:$port/_internal/gateway/plan",
            ['Content-Type' => 'application/json', 'X-Gateway-Internal' => WORKERMAN_INTERNAL_TOKEN],
            workermanPlanBody(),
        );

        expect($planned['status'])->toBe(200);

        $payload = json_decode($planned['body'], true, 512, JSON_THROW_ON_ERROR);
        expect($payload['version'])->toBe('v1')
            ->and($payload['action'])->toBe('relay')
            ->and($payload['relays'])->toHaveCount(1)
            ->and($payload['relays'][0]['subscriber_id'])->toBe(WORKERMAN_SUBSCRIBER_ID)
            ->and($payload['relays'][0]['url'])->toBe(WORKERMAN_RELAY_URL)
            ->and($payload['metadata']['endpoint_key'])->toBe(WORKERMAN_ENDPOINT_KEY);
    });
});

it('plans an unbound route as a pass-through', function (): void {
    workermanScenario([], function (array $handle, int $port): void {
        $planned = gatewayPlannerRequest(
            'POST',
            "http://127.0.0.1:$port/_internal/gateway/plan",
            ['Content-Type' => 'application/json', 'X-Gateway-Internal' => WORKERMAN_INTERNAL_TOKEN],
            workermanPlanBody('/unbound/path'),
        );

        expect($planned['status'])->toBe(200);
        $payload = json_decode($planned['body'], true, 512, JSON_THROW_ON_ERROR);
        expect($payload['action'])->toBe('pass_through');
    });
});

it('mutates and reads back a management resource with authorization', function (): void {
    workermanScenario([], function (array $handle, int $port): void {
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
    ], function (array $handle, int $port): void {
        $masterPid = $handle['masterPid'];

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
            gatewayPlannerRequest(
                'POST',
                "http://127.0.0.1:$port/_internal/gateway/plan",
                ['Content-Type' => 'application/json', 'X-Gateway-Internal' => WORKERMAN_INTERNAL_TOKEN],
                workermanPlanBody(),
            );

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
        expect(gatewayPlannerProcessAlive($masterPid))->toBeTrue();

        // Wait for the replacement worker to be ready, then confirm it serves a
        // valid database-backed plan.
        $after = null;
        $deadline = microtime(true) + 10.0;
        while (microtime(true) < $deadline) {
            $after = gatewayPlannerRequest(
                'POST',
                "http://127.0.0.1:$port/_internal/gateway/plan",
                ['Content-Type' => 'application/json', 'X-Gateway-Internal' => WORKERMAN_INTERNAL_TOKEN],
                workermanPlanBody(),
            );
            if ($after['status'] === 200) {
                break;
            }
            usleep(50_000);
        }
        expect($after['status'])->toBe(200);
        $payload = json_decode($after['body'], true, 512, JSON_THROW_ON_ERROR);
        expect($payload['action'])->toBe('relay')
            ->and($payload['relays'][0]['url'])->toBe(WORKERMAN_RELAY_URL);
    });
});

it('shuts down with bounded exit and refuses new connections afterwards', function (): void {
    $dataDirectory = gatewayPlannerDisposableDataDirectory();
    $database = $dataDirectory.'/gateway.sqlite';
    touch($database);

    gatewayPlannerWithEnvironment(
        gatewayPlannerBaseEnv($dataDirectory, $database),
        function () use ($dataDirectory, $database): void {
            gatewayPlannerRunMigrations($database);
            $port = gatewayPlannerFindFreePort();
            $handle = gatewayPlannerStartWorkerman($dataDirectory, $port);
            $masterPid = $handle['masterPid'];

            try {
                gatewayPlannerWaitForPort($port);
                expect(gatewayPlannerRequest('GET', "http://127.0.0.1:$port/health")['status'])->toBe(204);

                // Issue a request and terminate the master while the request is
                // in flight (the worker drains it during graceful shutdown).
                $socket = stream_socket_client("tcp://127.0.0.1:$port", $errno, $errstr, 5.0);
                expect($socket)->not->toBeFalse();
                fwrite($socket, "GET /health HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n");

                gatewayPlannerStopWorkerman($handle);

                // Read the in-flight response: the graceful drain must still
                // deliver it before the worker exits.
                $inFlight = '';
                while (! feof($socket)) {
                    $chunk = @fread($socket, 8192);
                    if ($chunk === false || $chunk === '') {
                        break;
                    }
                    $inFlight .= $chunk;
                }
                fclose($socket);
                expect($inFlight)->toContain('HTTP/1.1 204');

                // The master must exit within a bounded time (drain + exit).
                $deadline = microtime(true) + 5.0;
                while (microtime(true) < $deadline && gatewayPlannerProcessAlive($masterPid)) {
                    usleep(50_000);
                }
                expect(gatewayPlannerProcessAlive($masterPid))->toBeFalse();

                // New connections are refused after exit.
                $refused = false;
                for ($attempt = 0; $attempt < 20; $attempt++) {
                    try {
                        gatewayPlannerRequest('GET', "http://127.0.0.1:$port/health", [], '', 0.5);
                    } catch (Throwable) {
                        $refused = true;
                        break;
                    }
                    usleep(50_000);
                }
                expect($refused)->toBeTrue();
            } finally {
                gatewayPlannerStopWorkerman($handle);
            }
        },
    );

    gatewayPlannerRemoveDataDirectory($dataDirectory);
});

it('leaves an independently started host running after owned shutdown', function (): void {
    // Start an independent host directly (not via the owned helper).
    $independentDataDirectory = gatewayPlannerDisposableDataDirectory();
    $independentDatabase = $independentDataDirectory.'/gateway.sqlite';
    touch($independentDatabase);
    $independentPort = gatewayPlannerFindFreePort();

    gatewayPlannerWithEnvironment(
        gatewayPlannerBaseEnv($independentDataDirectory, $independentDatabase),
        function () use ($independentDataDirectory, $independentDatabase, $independentPort): void {
            gatewayPlannerRunMigrations($independentDatabase);

            // Configure the independent host's port BEFORE launching it.
            putenv("GATEWAY_PLANNER_PORT=$independentPort");
            putenv('GATEWAY_PLANNER_HOST=127.0.0.1');

            $independentProcess = proc_open(
                [PHP_BINARY, gatewayPlannerRepositoryRoot().'/app/workerman.php', 'start'],
                [
                    0 => ['file', '/dev/null', 'r'],
                    1 => ['file', $independentDataDirectory.'/storage/logs/independent.log', 'a'],
                    2 => ['file', $independentDataDirectory.'/storage/logs/independent.log', 'a'],
                ],
                $pipes,
                gatewayPlannerRepositoryRoot(),
                null,
            );
            expect($independentProcess)->toBeResource();
            foreach ($pipes as $pipe) {
                @fclose($pipe);
            }
            $independentMasterPid = (int) proc_get_status($independentProcess)['pid'];

            try {
                gatewayPlannerWaitForPort($independentPort);
                expect(gatewayPlannerRequest('GET', "http://127.0.0.1:$independentPort/health")['status'])->toBe(204);

                // Run an owned host and shut it down; the independent host must survive.
                workermanScenario([], function (array $handle, int $port): void {
                    expect(gatewayPlannerRequest('GET', "http://127.0.0.1:$port/health")['status'])->toBe(204);
                });

                expect(gatewayPlannerProcessAlive($independentMasterPid))->toBeTrue();
                expect(gatewayPlannerRequest('GET', "http://127.0.0.1:$independentPort/health")['status'])->toBe(204);
            } finally {
                @proc_terminate($independentProcess, SIGTERM);
                usleep(250_000);
                @proc_close($independentProcess);
            }
        },
    );

    gatewayPlannerRemoveDataDirectory($independentDataDirectory);
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

it('recovers after a real server-side database disconnection on PostgreSQL', function (): void {
    $connection = gatewayPlannerPostgresConnection();
    if ($connection === null) {
        $this->markTestSkipped('Set GATEWAY_TEST_POSTGRES_DATABASE to run the PostgreSQL disconnection recovery test.');
    }

    // Provision a truly disposable database so the test never touches an
    // existing application or shared test database.
    $databaseName = 'gateway_disconnect_'.bin2hex(random_bytes(8));
    $adminPdo = new PDO(sprintf(
        'pgsql:host=%s;port=%d;dbname=postgres',
        $connection['host'],
        $connection['port'],
    ), $connection['username'], $connection['password']);
    $adminPdo->exec('CREATE DATABASE "'.$databaseName.'"');

    $dataDirectory = gatewayPlannerDisposableDataDirectory();
    $disposableConnection = [
        'host' => $connection['host'],
        'port' => $connection['port'],
        'database' => $databaseName,
        'username' => $connection['username'],
        'password' => $connection['password'],
    ];
    $baseEnv = gatewayPlannerBaseEnvPostgres($dataDirectory, $disposableConnection);

    try {
        gatewayPlannerWithEnvironment(
            $baseEnv,
            function () use ($dataDirectory, $disposableConnection, $baseEnv): void {
                gatewayPlannerRunMigrationsPostgres($disposableConnection);
                gatewayPlannerSeedEndpoint(
                    $baseEnv,
                    WORKERMAN_ENDPOINT_KEY,
                    'application',
                    'app-123',
                    WORKERMAN_RELAY_URL,
                    WORKERMAN_SUBSCRIBER_ID,
                );

                $port = gatewayPlannerFindFreePort();
                $handle = gatewayPlannerStartWorkerman($dataDirectory, $port, [
                    'GATEWAY_PLANNER_WORKERS' => '1',
                    'GATEWAY_PLANNER_MAX_REQUESTS' => '1000',
                ]);
                try {
                    gatewayPlannerWaitForPort($port);

                    $plan = function () use ($port): array {
                        return gatewayPlannerRequest(
                            'POST',
                            "http://127.0.0.1:$port/_internal/gateway/plan",
                            ['Content-Type' => 'application/json', 'X-Gateway-Internal' => WORKERMAN_INTERNAL_TOKEN],
                            workermanPlanBody(),
                        );
                    };

                    // Prime the worker's database connection with a valid plan.
                    expect($plan()['status'])->toBe(200);

                    // Terminate every backend connection the server holds (the
                    // worker's PDO) from a separate client connection, simulating
                    // a dropped database connection server-side.
                    $admin = new PDO(sprintf(
                        'pgsql:host=%s;port=%d;dbname=%s',
                        $disposableConnection['host'],
                        $disposableConnection['port'],
                        $disposableConnection['database'],
                    ), $disposableConnection['username'], $disposableConnection['password']);
                    $admin->exec(
                        "SELECT pg_terminate_backend(pid) FROM pg_stat_activity "
                        ."WHERE datname = '{$disposableConnection['database']}' AND pid <> pg_backend_pid()",
                    );

                    // The worker must transparently reconnect and serve a valid
                    // database-backed plan after the drop.
                    $after = null;
                    $deadline = microtime(true) + 10.0;
                    while (microtime(true) < $deadline) {
                        $after = $plan();
                        if ($after['status'] === 200) {
                            break;
                        }
                        usleep(50_000);
                    }
                    expect($after['status'])->toBe(200);
                    $payload = json_decode($after['body'], true, 512, JSON_THROW_ON_ERROR);
                    expect($payload['action'])->toBe('relay')
                        ->and($payload['relays'][0]['url'])->toBe(WORKERMAN_RELAY_URL);
                } finally {
                    gatewayPlannerStopWorkerman($handle);
                }
            },
        );
    } finally {
        gatewayPlannerRemoveDataDirectory($dataDirectory);
        try {
            $adminPdo->exec('DROP DATABASE IF EXISTS "'.$databaseName.'" WITH (FORCE)');
        } catch (Throwable) {
            // Best-effort cleanup of the disposable database.
        }
    }
});
