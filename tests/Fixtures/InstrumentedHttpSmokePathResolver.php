<?php

declare(strict_types=1);

namespace Webong\Gateway\Tests\Fixtures;

use Illuminate\Support\Facades\Config;
use RuntimeException;
use Webong\Gateway\Contracts\PathResolver;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\PathBinding;

/**
 * Test-only path resolver for the Workerman Planner tests.
 *
 * `/http-smoke/registry-ingress` resolves exactly like HttpSmokePathResolver. The
 * other paths run test hooks *inside* the request that a real host serves, so a
 * test can observe the worker and the database connection from the inside
 * without any production code path being altered:
 *
 *   /http-smoke/hold                       signal entry, hold until released, then plan
 *   /http-smoke/probe/{token}              record worker PID, database backend PID and
 *                                          transaction level (an uncached read), then plan
 *   /http-smoke/write-then-throw/{token}   write inside an open transaction, then throw
 *   /http-smoke/scope/set/{value}          set request-scoped state
 *   /http-smoke/scope/read/{token}         record whether earlier request-scoped state is visible
 *
 * Hooks report through files under GATEWAY_TEST_PROBE_DIR (and the hold hook
 * through GATEWAY_TEST_HOLD_ENTERED_FILE / GATEWAY_TEST_HOLD_RELEASE_FILE).
 */
final class InstrumentedHttpSmokePathResolver implements PathResolver
{
    public function resolve(IngressRequest $request): ?PathBinding
    {
        $path = $request->path;

        if ($path === '/http-smoke/registry-ingress') {
            return self::binding();
        }
        if ($path === '/http-smoke/hold') {
            self::hold();

            return self::binding();
        }
        if (preg_match('#^/http-smoke/probe/([a-z0-9-]+)$#', $path, $match) === 1) {
            self::probe($match[1]);

            return self::binding();
        }
        if (preg_match('#^/http-smoke/write-then-throw/([a-z0-9-]+)$#', $path, $match) === 1) {
            self::writeThenThrow($match[1]);
        }
        if (preg_match('#^/http-smoke/scope/(set|read)/([a-z0-9-]+)$#', $path, $match) === 1) {
            self::scope($match[1], $match[2]);

            return self::binding();
        }

        return null;
    }

    public static function failedWriteName(string $token): string
    {
        return 'failed-write-'.$token;
    }

    private static function binding(): PathBinding
    {
        return new PathBinding(
            endpointKey: 'http-smoke-endpoint',
            scope: 'application',
            key: 'app-123',
        );
    }

    private static function hold(): void
    {
        $entered = getenv('GATEWAY_TEST_HOLD_ENTERED_FILE');
        $release = getenv('GATEWAY_TEST_HOLD_RELEASE_FILE');
        if (is_string($entered) && $entered !== '') {
            file_put_contents($entered, (string) getmypid());
        }

        // Bounded so a test that never releases cannot wedge a worker forever.
        $deadline = microtime(true) + 60.0;
        while (is_string($release) && $release !== '' && ! is_file($release) && microtime(true) < $deadline) {
            usleep(10_000);
        }
    }

    private static function probe(string $token): void
    {
        $connection = app('db')->connection();
        $backendPid = null;
        if ($connection->getDriverName() === 'pgsql') {
            // An uncached read straight through the connection this worker holds.
            $backendPid = (int) $connection->selectOne('SELECT pg_backend_pid() AS pid')->pid;
        } else {
            $connection->selectOne('SELECT 1 AS ok');
        }

        self::record($token, [
            'worker_pid' => getmypid(),
            'backend_pid' => $backendPid,
            'transaction_level' => $connection->transactionLevel(),
            'sandbox' => spl_object_id(app()),
        ]);
    }

    private static function writeThenThrow(string $token): never
    {
        $connection = app('db')->connection();
        $connection->beginTransaction();
        $connection->table('targets')->insert([
            'id' => 'wtt-'.bin2hex(random_bytes(6)),
            'name' => self::failedWriteName($token),
            'driver' => 'local',
            'connection' => '[]',
            'worker_token_hash' => str_repeat('b', 64),
            'created_at' => now()->toDateTimeString(),
            'updated_at' => now()->toDateTimeString(),
        ]);
        self::record($token, ['transaction_level_at_throw' => $connection->transactionLevel()]);

        throw new RuntimeException('intentional failure inside an open transaction');
    }

    private static function scope(string $mode, string $value): void
    {
        if ($mode === 'set') {
            app()->instance('gateway.test.scoped-value', $value);
            Config::set('gateway.test.scoped-value', $value);
            self::record('scope-set-'.$value, ['set' => $value]);

            return;
        }

        self::record($value, [
            'instance_bound' => app()->bound('gateway.test.scoped-value'),
            'instance_value' => app()->bound('gateway.test.scoped-value') ? app('gateway.test.scoped-value') : null,
            'config_value' => Config::get('gateway.test.scoped-value'),
        ]);
    }

    /** @param array<string, mixed> $data */
    private static function record(string $token, array $data): void
    {
        $directory = getenv('GATEWAY_TEST_PROBE_DIR');
        if (! is_string($directory) || $directory === '') {
            return;
        }
        file_put_contents(rtrim($directory, '/').'/'.$token.'.json', json_encode($data, JSON_THROW_ON_ERROR));
    }
}
