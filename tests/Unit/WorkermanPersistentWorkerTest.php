<?php

declare(strict_types=1);

use Gateway\Router\Workerman\LaravelHandler;
use Illuminate\Container\Container;
use Illuminate\Database\SQLiteConnection;
use Illuminate\Support\Facades\Facade;
use Laravel\Octane\CurrentApplication;
use Webong\Gateway\Tests\Fixtures\InstrumentedHttpSmokePathResolver;
use Workerman\Protocols\Http\Request;
use Workerman\Protocols\Http\Response as WorkermanResponse;

require_once __DIR__.'/../Support/WorkermanPlannerHelper.php';
require_once dirname(__DIR__, 2).'/app/helpers/Workerman/LaravelHandler.php';

/**
 * Persistent-worker isolation tests.
 *
 * A single `LaravelHandler` instance boots one Octane worker, so every
 * `handle()` call is served by that same persistent worker. These tests prove
 * the worker keeps request-scoped state isolated (a fresh application sandbox
 * per request, sharing the same base application), rolls back any transaction a
 * request leaves open (including when one connection's rollback fails), recovers
 * from a request that fails inside a transaction, and keeps memory bounded under
 * repeated planning requests. A real server-side database disconnection is
 * covered separately in `tests/Feature/WorkermanPlannerTest.php` against
 * disposable PostgreSQL.
 */

const ISOLATION_INTERNAL_TOKEN = 'isolation-internal-token';
const ISOLATION_REGISTRY_TOKEN = 'isolation-registry-token';

/** An SQLite connection whose rollback can be made to fail, to simulate a broken connection. */
final class FailingRollbackSqliteConnection extends SQLiteConnection
{
    public function rollBack($toLevel = null)
    {
        throw new RuntimeException('simulated rollback failure');
    }
}

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
 * when setup, the scenario, or handler construction throws.
 *
 * @param callable(LaravelHandler, string): void $scenario receives the handler and data directory
 */
function withIsolatedHandler(callable $scenario): void
{
    gatewayPlannerWithDisposableDirectory(function (string $dataDirectory) use ($scenario): void {
        $database = $dataDirectory.'/gateway.sqlite';
        touch($database);

        $baseEnv = array_merge(gatewayPlannerBaseEnv($dataDirectory, $database), [
            'GATEWAY_INTERNAL_TOKEN' => ISOLATION_INTERNAL_TOKEN,
            'REGISTRY_TOKEN' => ISOLATION_REGISTRY_TOKEN,
        ]);

        $previousApp = Container::getInstance();
        $handler = null;
        try {
            gatewayPlannerWithEnvironment($baseEnv, function () use ($database, $baseEnv, $dataDirectory, $scenario, &$handler): void {
                gatewayPlannerRunMigrations($database);
                gatewayPlannerSeedEndpoint($baseEnv, 'http-smoke-endpoint', 'application', 'app-123', 'https://subscriber.example.test/hook', 'workspace-42');

                $handler = new LaravelHandler(gatewayPlannerRepositoryRoot().'/app/planner');
                $scenario($handler, $dataDirectory);
            });
        } finally {
            $handler?->terminate();
            if ($previousApp !== null) {
                Container::setInstance($previousApp);
                CurrentApplication::set($previousApp);
                Facade::setFacadeApplication($previousApp);
            }
            Facade::clearResolvedInstances();
        }
    });
}

/** @return Laravel\Octane\Worker */
function octaneWorkerOf(LaravelHandler $handler): Laravel\Octane\Worker
{
    $property = new ReflectionProperty(LaravelHandler::class, 'worker');
    $property->setAccessible(true);

    return $property->getValue($handler);
}

it('serves every request through one persistent worker with a fresh request sandbox', function () use ($planRequest, $statusOf): void {
    withIsolatedHandler(function (LaravelHandler $handler) use ($planRequest, $statusOf): void {
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
    withIsolatedHandler(function (LaravelHandler $handler) use ($planRequest, $statusOf): void {
        expect($statusOf($handler->handle($planRequest(ISOLATION_INTERNAL_TOKEN))))->toBe(200);
        expect($statusOf($handler->handle($planRequest(''))))->toBe(404);
        expect($statusOf($handler->handle($planRequest('wrong-token'))))->toBe(404);
        expect($statusOf($handler->handle($planRequest(ISOLATION_INTERNAL_TOKEN))))->toBe(200);
    });
});

it('does not let a value set during request A be seen by request B on the same worker', function () use ($planRequest, $statusOf): void {
    withIsolatedHandler(function (LaravelHandler $handler, string $dataDirectory) use ($planRequest, $statusOf): void {
        $baseApp = octaneWorkerOf($handler)->application();

        // Request A sets request-scoped state (a container instance and a config
        // value) on its own sandbox and observes it there.
        expect($statusOf($handler->handle($planRequest(ISOLATION_INTERNAL_TOKEN, '/http-smoke/scope/set/principal-a'))))->toBe(200);
        expect(gatewayPlannerReadProbe($dataDirectory, 'scope-set-principal-a'))->toBe(['set' => 'principal-a']);

        // Request B, served by the same worker, must see none of it.
        expect($statusOf($handler->handle($planRequest(ISOLATION_INTERNAL_TOKEN, '/http-smoke/scope/read/observer-b'))))->toBe(200);
        expect(gatewayPlannerReadProbe($dataDirectory, 'observer-b'))->toBe([
            'instance_bound' => false,
            'instance_value' => null,
            'config_value' => null,
        ]);

        // The persistent base application was never touched either.
        expect(octaneWorkerOf($handler)->application())->toBe($baseApp)
            ->and($baseApp->bound('gateway.test.scoped-value'))->toBeFalse()
            ->and($baseApp['config']->get('gateway.test.scoped-value'))->toBeNull();
    });
});

it('rolls back an unfinished transaction and its write between requests', function () use ($statusOf, $healthRequest): void {
    withIsolatedHandler(function (LaravelHandler $handler) use ($statusOf, $healthRequest): void {
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

it('recovers on the same worker after a request writes inside an open transaction and throws', function () use ($planRequest, $statusOf): void {
    withIsolatedHandler(function (LaravelHandler $handler, string $dataDirectory) use ($planRequest, $statusOf): void {
        $octaneWorker = octaneWorkerOf($handler);
        $baseApp = $octaneWorker->application();
        $connection = $baseApp->make('db')->connection();
        $token = 'boom-'.bin2hex(random_bytes(4));
        $failedWrite = InstrumentedHttpSmokePathResolver::failedWriteName($token);

        // A real request: it opens a transaction, inserts a row, then throws.
        $failed = $handler->handle($planRequest(ISOLATION_INTERNAL_TOKEN, '/http-smoke/write-then-throw/'.$token));
        expect($statusOf($failed))->toBe(500);
        // The write really happened inside the request's open transaction.
        expect(gatewayPlannerReadProbe($dataDirectory, $token))->toBe(['transaction_level_at_throw' => 1]);

        // The same worker then serves a successful database-backed request.
        $ok = $handler->handle($planRequest(ISOLATION_INTERNAL_TOKEN, '/http-smoke/registry-ingress'));
        expect($statusOf($ok))->toBe(200);
        $payload = json_decode(substr((string) $ok, (int) strpos((string) $ok, "\r\n\r\n") + 4), true, 512, JSON_THROW_ON_ERROR);
        expect($payload['action'])->toBe('relay')
            ->and($payload['relays'][0]['url'])->toBe('https://subscriber.example.test/hook');

        // The failed write is gone, the connection is clean, and it was the same
        // persistent worker throughout.
        expect($connection->table('targets')->where('name', $failedWrite)->exists())->toBeFalse()
            ->and($connection->transactionLevel())->toBe(0)
            ->and(octaneWorkerOf($handler)->application())->toBe($baseApp);
    });
});

it('keeps cleaning other connections when one connection fails to roll back', function () use ($statusOf, $healthRequest): void {
    withIsolatedHandler(function (LaravelHandler $handler, string $dataDirectory) use ($statusOf, $healthRequest): void {
        $app = octaneWorkerOf($handler)->application();
        $db = $app->make('db');

        $fileA = $dataDirectory.'/broken.sqlite';
        $fileB = $dataDirectory.'/healthy.sqlite';
        foreach ([$fileA, $fileB] as $file) {
            $pdo = new PDO('sqlite:'.$file);
            $pdo->exec('CREATE TABLE notes (id INTEGER PRIMARY KEY, note TEXT)');
        }
        foreach (['broken' => $fileA, 'healthy' => $fileB] as $name => $file) {
            $app['config']->set("database.connections.$name", ['driver' => 'sqlite', 'database' => $file, 'prefix' => '']);
        }
        // `broken` cannot roll back; `healthy` is an ordinary connection.
        $db->extend('broken', fn (array $config, string $name) => new FailingRollbackSqliteConnection(new PDO('sqlite:'.$config['database']), $config['database'], '', $config));

        // `broken` is created first, so a failure there used to end the cleanup
        // loop before `healthy` was reached.
        $broken = $db->connection('broken');
        $healthy = $db->connection('healthy');
        expect(array_keys($db->getConnections()))->toContain('broken', 'healthy');

        foreach ([$broken, $healthy] as $connection) {
            $connection->beginTransaction();
            $connection->table('notes')->insert(['note' => 'must not survive']);
            expect($connection->transactionLevel())->toBe(1);
        }

        expect($statusOf($handler->handle($healthRequest)))->toBe(204);

        // The healthy connection was still rolled back...
        expect($healthy->transactionLevel())->toBe(0)
            ->and($healthy->table('notes')->count())->toBe(0);
        // ...and the failing one was discarded rather than reused with its open transaction.
        expect(array_keys($db->getConnections()))->not->toContain('broken');

        // The worker keeps serving, and cleanup no longer revisits the discarded connection.
        expect($statusOf($handler->handle($healthRequest)))->toBe(204);
        expect(array_keys($db->getConnections()))->not->toContain('broken');
    });
});

it('keeps memory bounded under repeated database-backed planning requests', function () use ($planRequest, $statusOf): void {
    withIsolatedHandler(function (LaravelHandler $handler) use ($planRequest, $statusOf): void {
        $statusOf($handler->handle($planRequest(ISOLATION_INTERNAL_TOKEN, '/http-smoke/registry-ingress')));
        $baselineMemory = memory_get_usage(true);

        for ($request = 0; $request < 250; $request++) {
            expect($statusOf($handler->handle($planRequest(ISOLATION_INTERNAL_TOKEN, '/http-smoke/registry-ingress'))))->toBe(200);
        }
        gc_collect_cycles();

        expect(memory_get_usage(true) - $baselineMemory)->toBeLessThanOrEqual(8 * 1024 * 1024);
    });
});
