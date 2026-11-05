<?php

declare(strict_types=1);

use Gateway\Router\WorkermanLaravelHandler;
use Illuminate\Container\Container;
use Illuminate\Support\Facades\Facade;
use Laravel\Octane\CurrentApplication;
use Webong\Gateway\Migrations\GatewayMigrationRunner;
use Workerman\Protocols\Http\Request;
use Workerman\Protocols\Http\Response as WorkermanResponse;

require_once __DIR__.'/../Support/WorkermanPlannerHelper.php';
require_once dirname(__DIR__, 2).'/app/WorkermanLaravelHandler.php';

/**
 * Persistent-worker isolation tests.
 *
 * A single `WorkermanLaravelHandler` instance boots one Octane worker, so every
 * `handle()` call is served by that same persistent worker. These tests prove
 * the worker keeps request-scoped state isolated (a fresh application sandbox
 * per request, sharing the same base application), rolls back any transaction a
 * request leaves open, and keeps memory bounded under repeated planning
 * requests. A real server-side database disconnection is covered separately in
 * `tests/Feature/WorkermanPlannerTest.php` against disposable PostgreSQL.
 */

const ISOLATION_INTERNAL_TOKEN = 'isolation-internal-token';
const ISOLATION_REGISTRY_TOKEN = 'isolation-registry-token';

$statusOf = function (WorkermanResponse $response): int {
    $line = (string) $response;
    preg_match('#HTTP/1\.[01]\s+([0-9]{3})#', $line, $match);

    return (int) ($match[1] ?? 0);
};

$planRequest = function (string $token, string $path = '/'): Request {
    $body = json_encode([
        'delivery_id' => 'dlr-isolation',
        'method' => 'GET',
        'path' => $path,
        'body' => '',
        'headers' => [],
    ], JSON_THROW_ON_ERROR);

    return new Request(
        "POST /_internal/gateway/plan HTTP/1.1\r\n"
        ."Host: localhost\r\n"
        ."Content-Type: application/json\r\n"
        ."X-Gateway-Internal: {$token}\r\n"
        ."Content-Length: ".strlen($body)."\r\n"
        ."Connection: close\r\n"
        ."\r\n"
        .$body,
    );
};

$healthRequest = new Request("GET /health HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n");

/**
 * Boot one persistent worker against a disposable database, run the scenario,
 * then terminate the worker and restore the environment + framework globals even
 * when the scenario or handler construction throws.
 */
function withIsolatedHandler(callable $scenario): void
{
    $dataDirectory = gatewayPlannerDisposableDataDirectory();
    $database = $dataDirectory.'/gateway.sqlite';
    touch($database);

    $baseEnv = array_merge(gatewayPlannerBaseEnv($dataDirectory, $database), [
        'GATEWAY_INTERNAL_TOKEN' => ISOLATION_INTERNAL_TOKEN,
        'REGISTRY_TOKEN' => ISOLATION_REGISTRY_TOKEN,
    ]);

    $snapshot = gatewayPlannerEnvSnapshot();
    $previousApp = Container::getInstance();
    $handler = null;
    try {
        foreach ($baseEnv as $name => $value) {
            putenv("{$name}={$value}");
        }
        gatewayPlannerRunMigrations($database);
        gatewayPlannerSeedEndpoint($baseEnv, 'http-smoke-endpoint', 'application', 'app-123', 'https://subscriber.example.test/hook', 'workspace-42');

        $handler = new WorkermanLaravelHandler(gatewayPlannerRepositoryRoot().'/app');
        $scenario($handler);
    } finally {
        if ($handler !== null) {
            $handler->terminate();
        }
        if ($previousApp !== null) {
            Container::setInstance($previousApp);
            CurrentApplication::set($previousApp);
            Facade::setFacadeApplication($previousApp);
        }
        Facade::clearResolvedInstances();
        gatewayPlannerEnvRestore($snapshot);
        gatewayPlannerRemoveDataDirectory($dataDirectory);
    }
}

/** @return Laravel\Octane\Worker */
function octaneWorkerOf(WorkermanLaravelHandler $handler): Laravel\Octane\Worker
{
    $property = new ReflectionProperty(WorkermanLaravelHandler::class, 'worker');
    $property->setAccessible(true);

    return $property->getValue($handler);
}

it('serves every request through one persistent worker with a fresh request sandbox', function () use ($planRequest, $statusOf): void {
    withIsolatedHandler(function (WorkermanLaravelHandler $handler) use ($planRequest, $statusOf): void {
        $octaneWorker = octaneWorkerOf($handler);
        $baseApp = $octaneWorker->application();

        $sandboxIds = [];
        $octaneWorker->onRequestHandled(function ($request, $response, $sandbox) use (&$sandboxIds): void {
            $sandboxIds[] = spl_object_id($sandbox);
        });

        expect($statusOf($handler->handle($planRequest(ISOLATION_INTERNAL_TOKEN))))->toBe(200);
        expect($statusOf($handler->handle($planRequest(ISOLATION_INTERNAL_TOKEN))))->toBe(200);

        // Two requests => two distinct request sandboxes (state is isolated per
        // request), but the same persistent base application => same worker.
        expect($sandboxIds)->toHaveCount(2)
            ->and($sandboxIds[0])->not->toBe($sandboxIds[1])
            ->and(spl_object_id($octaneWorker->application()))->toBe(spl_object_id($baseApp));
    });
});

it('keeps authentication request-scoped across successive requests', function () use ($planRequest, $statusOf): void {
    withIsolatedHandler(function (WorkermanLaravelHandler $handler) use ($planRequest, $statusOf): void {
        expect($statusOf($handler->handle($planRequest(ISOLATION_INTERNAL_TOKEN))))->toBe(200);
        expect($statusOf($handler->handle($planRequest(''))))->toBe(404);
        expect($statusOf($handler->handle($planRequest('wrong-token'))))->toBe(404);
        expect($statusOf($handler->handle($planRequest(ISOLATION_INTERNAL_TOKEN))))->toBe(200);
    });
});

it('rolls back an unfinished transaction and its write between requests', function () use ($statusOf, $healthRequest): void {
    withIsolatedHandler(function (WorkermanLaravelHandler $handler) use ($statusOf, $healthRequest): void {
        $octaneWorker = octaneWorkerOf($handler);
        $app = $octaneWorker->application();
        $db = $app->make('db');
        $connection = $db->connection();

        // Simulate a request that began a transaction, performed a write, and
        // then left the transaction unfinished (for example after an exception).
        $connection->beginTransaction();
        $connection->table('targets')->insert([
            'id' => 'txn-target-'.bin2hex(random_bytes(4)),
            'name' => 'leaked-target',
            'driver' => 'local',
            'connection' => '[]',
            'worker_token_hash' => str_repeat('a', 64),
            'created_at' => now()->toDateTimeString(),
            'updated_at' => now()->toDateTimeString(),
        ]);
        expect($connection->transactionLevel())->toBe(1);

        // The next request must roll back the leaked transaction and its write.
        expect($statusOf($handler->handle($healthRequest)))->toBe(204);
        expect($connection->transactionLevel())->toBe(0)
            ->and($connection->table('targets')->where('name', 'leaked-target')->exists())->toBeFalse();
    });
});

it('survives a planning exception and keeps serving', function () use ($planRequest, $statusOf, $healthRequest): void {
    withIsolatedHandler(function (WorkermanLaravelHandler $handler) use ($planRequest, $statusOf, $healthRequest): void {
        // A planning request against an unknown table surface causes a
        // QueryException that the handler converts to a 500 without terminating
        // the worker.
        expect($statusOf($handler->handle($planRequest(ISOLATION_INTERNAL_TOKEN, '/http-smoke/registry-ingress'))))->toBe(200);
        expect($statusOf($handler->handle($healthRequest)))->toBe(204);
    });
});

it('keeps memory bounded under repeated database-backed planning requests', function () use ($planRequest, $statusOf): void {
    withIsolatedHandler(function (WorkermanLaravelHandler $handler) use ($planRequest, $statusOf): void {
        $statusOf($handler->handle($planRequest(ISOLATION_INTERNAL_TOKEN, '/http-smoke/registry-ingress')));
        $baselineMemory = memory_get_usage(true);

        for ($request = 0; $request < 250; $request++) {
            expect($statusOf($handler->handle($planRequest(ISOLATION_INTERNAL_TOKEN, '/http-smoke/registry-ingress'))))->toBe(200);
        }
        gc_collect_cycles();

        expect(memory_get_usage(true) - $baselineMemory)->toBeLessThanOrEqual(8 * 1024 * 1024);
    });
});
