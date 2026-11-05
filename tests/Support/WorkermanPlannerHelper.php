<?php

declare(strict_types=1);

/**
 * Shared helpers for Workerman Planner integration tests.
 *
 * These tests start a real `app/workerman.php` process against a disposable
 * SQLite (or, when configured, PostgreSQL) database and exercise it over HTTP.
 * They never touch an existing application database: every schema change and
 * every seeded endpoint/subscription runs against a freshly created, randomly
 * named database that is deleted when the test finishes.
 *
 * Process ownership is strict: helpers return a handle describing the exact
 * master process they launched, and shutdown terminates only that process tree
 * (never a global `pkill` by name), so an independently started host is never
 * disturbed.
 */

if (! function_exists('gatewayPlannerRepositoryRoot')) {
    function gatewayPlannerRepositoryRoot(): string
    {
        return dirname(__DIR__, 2);
    }
}

// ---------------------------------------------------------------------------
// Environment isolation
// ---------------------------------------------------------------------------

if (! function_exists('gatewayPlannerEnvSnapshot')) {
    /**
     * Snapshot the process environment and the affected superglobals so a test
     * can restore them afterwards. Covers `putenv()`/`getenv()`, `$_ENV`, and
     * `$_SERVER`, including cache paths the app bootstrap writes into `$_ENV`.
     *
     * @return array{getenv: array<string, string>, env: array<string, mixed>, server: array<string, mixed>}
     */
    function gatewayPlannerEnvSnapshot(): array
    {
        return [
            'getenv' => getenv() ?: [],
            'env' => $_ENV,
            'server' => $_SERVER,
        ];
    }
}

if (! function_exists('gatewayPlannerEnvRestore')) {
    /**
     * @param array{getenv: array<string, string>, env: array<string, mixed>, server: array<string, mixed>} $snapshot
     */
    function gatewayPlannerEnvRestore(array $snapshot): void
    {
        // Remove any process-environment variable added since the snapshot.
        foreach (array_keys(getenv() ?: []) as $name) {
            if (! array_key_exists($name, $snapshot['getenv'])) {
                putenv((string) $name);
            }
        }
        // Restore the original values.
        foreach ($snapshot['getenv'] as $name => $value) {
            putenv("{$name}={$value}");
        }

        $_ENV = $snapshot['env'];
        $_SERVER = $snapshot['server'];
    }
}

if (! function_exists('gatewayPlannerWithEnvironment')) {
    /**
     * Apply an environment override, run the body, and restore the previous
     * environment even when the body throws.
     *
     * @param array<string, string> $env
     * @param callable(): mixed $body
     */
    function gatewayPlannerWithEnvironment(array $env, callable $body): mixed
    {
        $snapshot = gatewayPlannerEnvSnapshot();
        try {
            foreach ($env as $name => $value) {
                putenv("{$name}={$value}");
            }

            return $body();
        } finally {
            gatewayPlannerEnvRestore($snapshot);
        }
    }
}

// ---------------------------------------------------------------------------
// Disposable data directory + database
// ---------------------------------------------------------------------------

if (! function_exists('gatewayPlannerDisposableDataDirectory')) {
    function gatewayPlannerDisposableDataDirectory(): string
    {
        $directory = sys_get_temp_dir().'/gateway-planner-workerman-'.bin2hex(random_bytes(8));
        mkdir($directory, 0700, true);
        foreach ([
            'cache',
            'storage/framework/cache/data',
            'storage/framework/sessions',
            'storage/framework/views',
            'storage/logs',
        ] as $subdirectory) {
            mkdir($directory.'/'.$subdirectory, 0700, true);
        }

        return $directory;
    }
}

if (! function_exists('gatewayPlannerRemoveDataDirectory')) {
    function gatewayPlannerRemoveDataDirectory(string $directory): void
    {
        if (! is_dir($directory)) {
            return;
        }
        (new Illuminate\Filesystem\Filesystem())->deleteDirectory($directory);
    }
}

if (! function_exists('gatewayPlannerBaseEnv')) {
    /**
     * The environment every Workerman Planner host needs for a disposable
     * database. Add server-specific variables via `gatewayPlannerWithEnvironment`.
     *
     * @return array<string, string>
     */
    function gatewayPlannerBaseEnv(string $dataDirectory, string $database, string $dbConnection = 'sqlite'): array
    {
        return [
            'APP_KEY' => 'base64:'.base64_encode(random_bytes(32)),
            'SESSION_DRIVER' => 'array',
            'GATEWAY_INTERNAL_TOKEN' => 'gateway-internal-test-token',
            'REGISTRY_TOKEN' => 'registry-test-token',
            'DB_CONNECTION' => $dbConnection,
            'DB_DATABASE' => $database,
            'GATEWAY_ROUTER_DATA_DIR' => $dataDirectory,
            'LARAVEL_STORAGE_PATH' => $dataDirectory.'/storage',
            'APP_RUNNING_IN_CONSOLE' => 'false',
            'GATEWAY_PATH_RESOLVER' => 'Webong\\Gateway\\Tests\\Fixtures\\HttpSmokePathResolver',
        ];
    }
}

// ---------------------------------------------------------------------------
// Migrations + seeding
// ---------------------------------------------------------------------------

if (! function_exists('gatewayPlannerRunMigrations')) {
    /**
     * Run every Planner + proxy + application migration against a disposable
     * SQLite database file.
     */
    function gatewayPlannerRunMigrations(string $database): void
    {
        $root = gatewayPlannerRepositoryRoot();

        $runner = Webong\Gateway\Migrations\GatewayMigrationRunner::sqlite(
            database: $database,
            migrationsPath: $root.'/pkg/planner/migrations',
            tables: [],
        );
        $runner->migrate();

        $artisan = $root.'/app/artisan';
        $proxyMigrations = $root.'/vendor/webong/proxy/database/migrations';
        foreach ([
            "php $artisan migrate --realpath --path=$proxyMigrations --force --no-ansi",
            "php $artisan migrate --force --no-ansi",
        ] as $command) {
            $exitCode = null;
            $output = null;
            exec($command.' 2>&1', $output, $exitCode);
            if ($exitCode !== 0) {
                throw new RuntimeException(
                    "Artisan migration failed (exit $exitCode):\n".implode("\n", $output),
                );
            }
        }
    }
}

if (! function_exists('gatewayPlannerPostgresConnection')) {
    /**
     * Read the disposable PostgreSQL connection from the environment, or return
     * null when PostgreSQL is not configured (tests should then skip).
     *
     * @return array{driver: string, host: string, port: int, database: string, username: string, password: string}|null
     */
    function gatewayPlannerPostgresConnection(): ?array
    {
        $database = getenv('GATEWAY_TEST_POSTGRES_DATABASE');
        if (! is_string($database) || $database === '') {
            return null;
        }

        return [
            'driver' => 'pgsql',
            'host' => getenv('GATEWAY_TEST_POSTGRES_HOST') ?: '127.0.0.1',
            'port' => (int) (getenv('GATEWAY_TEST_POSTGRES_PORT') ?: 5432),
            'database' => $database,
            'username' => getenv('GATEWAY_TEST_POSTGRES_USERNAME') ?: 'postgres',
            'password' => getenv('GATEWAY_TEST_POSTGRES_PASSWORD') ?: '',
        ];
    }
}

if (! function_exists('gatewayPlannerRunMigrationsPostgres')) {
    /**
     * Run every Planner + proxy + application migration against a disposable
     * PostgreSQL database.
     *
     * @param array{host: string, port: int, database: string, username: string, password: string} $connection
     */
    function gatewayPlannerRunMigrationsPostgres(array $connection): void
    {
        $root = gatewayPlannerRepositoryRoot();

        $runner = Webong\Gateway\Migrations\GatewayMigrationRunner::postgres(
            host: $connection['host'],
            port: (int) $connection['port'],
            database: $connection['database'],
            username: $connection['username'],
            password: $connection['password'],
            migrationsPath: $root.'/pkg/planner/migrations',
            tables: [],
        );
        $runner->migrate();

        $artisan = $root.'/app/artisan';
        $proxyMigrations = $root.'/vendor/webong/proxy/database/migrations';
        foreach ([
            "php $artisan migrate --realpath --path=$proxyMigrations --force --no-ansi",
            "php $artisan migrate --force --no-ansi",
        ] as $command) {
            $exitCode = null;
            $output = null;
            exec($command.' 2>&1', $output, $exitCode);
            if ($exitCode !== 0) {
                throw new RuntimeException(
                    "Artisan migration failed (exit $exitCode):\n".implode("\n", $output),
                );
            }
        }
    }
}

if (! function_exists('gatewayPlannerBaseEnvPostgres')) {
    /**
     * The environment a Workerman Planner host needs for a disposable PostgreSQL
     * database.
     *
     * @param array{host: string, port: int, database: string, username: string, password: string} $connection
     * @return array<string, string>
     */
    function gatewayPlannerBaseEnvPostgres(string $dataDirectory, array $connection): array
    {
        return [
            'APP_KEY' => 'base64:'.base64_encode(random_bytes(32)),
            'SESSION_DRIVER' => 'array',
            'GATEWAY_INTERNAL_TOKEN' => 'gateway-internal-test-token',
            'REGISTRY_TOKEN' => 'registry-test-token',
            'DB_CONNECTION' => 'pgsql',
            'DB_HOST' => $connection['host'],
            'DB_PORT' => (string) $connection['port'],
            'DB_DATABASE' => $connection['database'],
            'DB_USERNAME' => $connection['username'],
            'DB_PASSWORD' => $connection['password'],
            'GATEWAY_ROUTER_DATA_DIR' => $dataDirectory,
            'LARAVEL_STORAGE_PATH' => $dataDirectory.'/storage',
            'APP_RUNNING_IN_CONSOLE' => 'false',
            'GATEWAY_PATH_RESOLVER' => 'Webong\\Gateway\\Tests\\Fixtures\\HttpSmokePathResolver',
        ];
    }
}

if (! function_exists('gatewayPlannerSeedEndpoint')) {
    /**
     * Seed a managed endpoint + relay subscription into the configured database
     * by booting the real application (reuses EnsureEndpoint/SubscribeEndpoint
     * so the metadata format is exactly what production uses).
     *
     * @param array<string, string> $env the environment to seed under (must include
     *                                   DB_* and the app/cache paths).
     */
    function gatewayPlannerSeedEndpoint(
        array $env,
        string $endpointKey = 'http-smoke-endpoint',
        string $routingScope = 'application',
        string $routingKey = 'app-123',
        string $url = 'https://subscriber.example.test/hook',
        string $subscriberId = 'workspace-42',
    ): void {
        $root = gatewayPlannerRepositoryRoot();
        $script = $root.'/tests/Support/seed-planner-endpoint.php';

        gatewayPlannerWithEnvironment(array_merge($env, [
            'GATEWAY_SEED_ENDPOINT_KEY' => $endpointKey,
            'GATEWAY_SEED_EXTERNAL_ID' => 'provider-app-123',
            'GATEWAY_SEED_ROUTING_SCOPE' => $routingScope,
            'GATEWAY_SEED_ROUTING_KEY' => $routingKey,
            'GATEWAY_SEED_SUBSCRIBER_ID' => $subscriberId,
            'GATEWAY_SEED_URL' => $url,
        ]), function () use ($script): void {
            $exitCode = null;
            $output = [];
            exec(escapeshellarg(PHP_BINARY).' '.escapeshellarg($script).' 2>&1', $output, $exitCode);
            if ($exitCode !== 0) {
                throw new RuntimeException(
                    "Endpoint seeding failed (exit $exitCode):\n".implode("\n", $output),
                );
            }
        });
    }
}

// ---------------------------------------------------------------------------
// Process management (owned only)
// ---------------------------------------------------------------------------

if (! function_exists('gatewayPlannerFindFreePort')) {
    function gatewayPlannerFindFreePort(): int
    {
        $socket = stream_socket_server('tcp://127.0.0.1:0');
        if ($socket === false) {
            throw new RuntimeException('Unable to allocate a free TCP port.');
        }
        $address = stream_socket_get_name($socket, false);
        fclose($socket);
        if ($address === false || ! preg_match('/:(\d+)$/', $address, $match)) {
            throw new RuntimeException('Unable to determine allocated port.');
        }

        return (int) $match[1];
    }
}

if (! function_exists('gatewayPlannerWaitForPort')) {
    function gatewayPlannerWaitForPort(int $port, float $timeoutSeconds = 15.0): void
    {
        $deadline = microtime(true) + $timeoutSeconds;
        $lastError = '';
        while (microtime(true) < $deadline) {
            $socket = @stream_socket_client('tcp://127.0.0.1:'.$port, $errno, $errstr, 0.2);
            if ($socket !== false) {
                fclose($socket);

                return;
            }
            $lastError = $errstr;
            usleep(50_000);
        }
        throw new RuntimeException("Port 127.0.0.1:$port did not accept connections: $lastError");
    }
}

if (! function_exists('gatewayPlannerProcessAlive')) {
    function gatewayPlannerProcessAlive(int $pid): bool
    {
        return $pid > 0 && posix_kill($pid, 0);
    }
}

if (! function_exists('gatewayPlannerWorkerPids')) {
    /**
     * Return the Workerman worker (child) PIDs for a master process.
     *
     * @return list<int>
     */
    function gatewayPlannerWorkerPids(int $masterPid): array
    {
        $output = [];
        exec('pgrep -P '.escapeshellarg((string) $masterPid).' 2>/dev/null', $output);
        $pids = [];
        foreach ($output as $line) {
            if (ctype_digit(trim($line))) {
                $pids[] = (int) trim($line);
            }
        }

        return $pids;
    }
}

if (! function_exists('gatewayPlannerStartWorkerman')) {
    /**
     * Start `app/workerman.php` as a detached background process.
     *
     * @param array<string, string> $extraEnv server variables (workers, max requests, port).
     * @return array{process: resource, masterPid: int, logFile: string}
     */
    function gatewayPlannerStartWorkerman(string $dataDirectory, int $port, array $extraEnv = []): array
    {
        $root = gatewayPlannerRepositoryRoot();
        foreach ($extraEnv as $name => $value) {
            putenv("{$name}={$value}");
        }
        putenv("GATEWAY_PLANNER_PORT={$port}");
        putenv('GATEWAY_PLANNER_HOST=127.0.0.1');
        putenv('APP_RUNNING_IN_CONSOLE=false');

        $logFile = $dataDirectory.'/storage/logs/gateway-planner-test.log';
        $descriptors = [
            0 => ['file', '/dev/null', 'r'],
            1 => ['file', $logFile, 'a'],
            2 => ['file', $logFile, 'a'],
        ];

        $process = proc_open(
            [PHP_BINARY, "{$root}/app/workerman.php", 'start'],
            $descriptors,
            $pipes,
            $root,
            null,
        );
        if (! is_resource($process)) {
            throw new RuntimeException('Workerman failed to start: proc_open returned no process.');
        }
        foreach ($pipes as $pipe) {
            @fclose($pipe);
        }

        $status = proc_get_status($process);
        if ($status === false || ! isset($status['pid']) || ! ctype_digit((string) $status['pid'])) {
            @proc_close($process);
            throw new RuntimeException('Workerman failed to start: unable to determine master PID.');
        }

        return [
            'process' => $process,
            'masterPid' => (int) $status['pid'],
            'logFile' => $logFile,
        ];
    }
}

if (! function_exists('gatewayPlannerStopWorkerman')) {
    /**
     * Stop a Workerman host we own. Terminates only the given master process
     * and its descendants. Never touches unrelated processes.
     *
     * @param array{process: resource, masterPid: int, logFile: string} $handle
     */
    function gatewayPlannerStopWorkerman(array $handle): void
    {
        $masterPid = (int) ($handle['masterPid'] ?? 0);
        $process = $handle['process'] ?? null;

        if ($masterPid <= 0) {
            return;
        }

        // 1. Graceful stop: SIGTERM the master, which drains and stops its workers.
        if (is_resource($process)) {
            @proc_terminate($process, SIGTERM);
        } else {
            @posix_kill($masterPid, SIGTERM);
        }

        // 2. Bounded wait for the master to exit.
        $deadline = microtime(true) + 5.0;
        while (microtime(true) < $deadline && gatewayPlannerProcessAlive($masterPid)) {
            usleep(50_000);
        }

        // 3. Owned fallback: force-kill any surviving master or workers we launched.
        if (gatewayPlannerProcessAlive($masterPid)) {
            foreach (gatewayPlannerWorkerPids($masterPid) as $workerPid) {
                @posix_kill($workerPid, SIGKILL);
            }
            @posix_kill($masterPid, SIGKILL);
        }

        if (is_resource($process)) {
            @proc_close($process);
        }
    }
}

// ---------------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------------

if (! function_exists('gatewayPlannerRequest')) {
    /**
     * Make a raw HTTP/1.1 request and return a structured response.
     *
     * @return array{status: int, body: string, headers: array<string, string>, raw: string}
     */
    function gatewayPlannerRequest(
        string $method,
        string $uri,
        array $headers = [],
        string $body = '',
        float $timeoutSeconds = 5.0,
    ): array {
        $parsed = parse_url($uri);
        $host = $parsed['host'] ?? '127.0.0.1';
        $port = $parsed['port'] ?? 80;
        $path = ($parsed['path'] ?? '/') ?: '/';
        if (! empty($parsed['query'])) {
            $path .= '?'.$parsed['query'];
        }

        $socket = @stream_socket_client('tcp://'.$host.':'.$port, $errno, $errstr, $timeoutSeconds);
        if ($socket === false) {
            throw new RuntimeException("Unable to connect to $host:$port: $errstr ($errno)");
        }
        $seconds = (int) $timeoutSeconds;
        $microseconds = (int) (($timeoutSeconds - $seconds) * 1_000_000);
        stream_set_timeout($socket, $seconds, $microseconds);

        $start = microtime(true);
        $request = "$method $path HTTP/1.1\r\n";
        $request .= "Host: $host\r\n";
        $request .= "Connection: close\r\n";
        foreach ($headers as $name => $value) {
            $request .= "$name: $value\r\n";
        }
        if ($body !== '') {
            $request .= 'Content-Length: '.strlen($body)."\r\n";
        }
        $request .= "\r\n".$body;
        fwrite($socket, $request);

        $response = '';
        while (strpos($response, "\r\n\r\n") === false) {
            $chunk = @fread($socket, 8192);
            if ($chunk === false || $chunk === '') {
                break;
            }
            $response .= $chunk;
        }

        $headEnd = strpos($response, "\r\n\r\n");
        $head = $headEnd === false ? $response : substr($response, 0, $headEnd);
        $contentLength = 0;
        foreach (explode("\r\n", $head) as $headerLine) {
            if (stripos($headerLine, 'Content-Length:') === 0) {
                $contentLength = (int) trim(substr($headerLine, 16));
            }
        }
        $bodyStart = $headEnd === false ? strlen($response) : ($headEnd + 4);
        $receivedBody = substr($response, $bodyStart);
        while ($contentLength > 0 && strlen($receivedBody) < $contentLength) {
            $chunk = @fread($socket, min(8192, $contentLength - strlen($receivedBody)));
            if ($chunk === false || $chunk === '') {
                break;
            }
            $receivedBody .= $chunk;
        }
        fclose($socket);

        $status = 0;
        if (preg_match('#HTTP/1\.[01]\s+([0-9]{3})#', $response, $match)) {
            $status = (int) $match[1];
        }

        $responseHeaders = [];
        foreach (explode("\r\n", $head) as $headerLine) {
            if (preg_match('/^([^:]+):\s*(.*)$/', $headerLine, $match)) {
                $responseHeaders[strtolower($match[1])] = $match[2];
            }
        }

        return [
            'status' => $status,
            'body' => $receivedBody,
            'headers' => $responseHeaders,
            'raw' => $response,
            'elapsed' => microtime(true) - $start,
        ];
    }
}
