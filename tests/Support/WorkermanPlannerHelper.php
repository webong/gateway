<?php

declare(strict_types=1);

/**
 * Shared helpers for Workerman Planner integration tests.
 *
 * These tests start a real `app/servers/workerman/server.php` process against a disposable
 * SQLite (or, when configured, PostgreSQL) database and exercise it over HTTP.
 * They never touch an existing application database: every schema change and
 * every seeded endpoint/subscription runs against a freshly created, randomly
 * named database that is deleted when the test finishes.
 *
 * Database protection has two layers:
 *   1. The environment is isolated. Inherited database and Laravel cache-path
 *      settings (DB_URL, APP_CONFIG_CACHE, ...) are removed or redirected into the
 *      disposable data directory for this PHP process and every child.
 *   2. Before the first write, the database Laravel actually resolves is read back
 *      and compared with the intended one (file path for SQLite; database name and
 *      server identity for PostgreSQL). Any mismatch aborts before migrations,
 *      seeding or host startup.
 *
 * Process ownership is strict: helpers return a handle describing the exact
 * master process they launched, and shutdown signals only that process tree
 * (never a global `pkill` by name, and never a saved PID once the handle is
 * closed), so an independently started host is never disturbed.
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
     * A `null` value removes the variable from the process environment and from
     * `$_ENV`/`$_SERVER`, so neither child processes nor in-process Laravel (which
     * reads `$_ENV`, then `$_SERVER`, then `getenv()`) can see an inherited value.
     *
     * @param array<string, string|null> $env
     * @param callable(): mixed $body
     */
    function gatewayPlannerWithEnvironment(array $env, callable $body): mixed
    {
        $snapshot = gatewayPlannerEnvSnapshot();
        try {
            foreach ($env as $name => $value) {
                if ($value === null) {
                    putenv((string) $name);
                    unset($_ENV[$name], $_SERVER[$name]);

                    continue;
                }
                putenv("{$name}={$value}");
                $_ENV[$name] = $value;
                $_SERVER[$name] = $value;
            }

            return $body();
        } finally {
            gatewayPlannerEnvRestore($snapshot);
        }
    }
}

if (! function_exists('gatewayPlannerIsolatedEnvironment')) {
    /**
     * Settings that must not leak into a disposable run from the inherited
     * environment: anything that can redirect the database connection, plus every
     * Laravel cache path (a cached config bakes the database in, and would be loaded
     * instead of the environment). Database variables are removed so only the
     * explicit values a base environment sets apply; cache paths are redirected
     * into the disposable data directory, where no stale cache exists.
     *
     * @return array<string, string|null>
     */
    function gatewayPlannerIsolatedEnvironment(string $dataDirectory): array
    {
        $environment = array_fill_keys([
            'DB_CONNECTION', 'DB_URL', 'DATABASE_URL', 'DB_HOST', 'DB_PORT', 'DB_DATABASE',
            'DB_USERNAME', 'DB_PASSWORD', 'DB_SOCKET', 'DB_CHARSET', 'DB_COLLATION', 'DB_SSLMODE',
            'DB_TIMEZONE', 'DB_ISOLATION_LEVEL', 'MYSQL_ATTR_SSL_CA',
        ], null);

        foreach ([
            'APP_CONFIG_CACHE' => 'config.php',
            'APP_ROUTES_CACHE' => 'routes-v7.php',
            'APP_SERVICES_CACHE' => 'services.php',
            'APP_PACKAGES_CACHE' => 'packages.php',
            'APP_EVENTS_CACHE' => 'events.php',
        ] as $name => $file) {
            $environment[$name] = $dataDirectory.'/cache/'.$file;
        }

        return $environment;
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
            'probes',
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

if (! function_exists('gatewayPlannerWithDisposableDirectory')) {
    /**
     * Create a disposable data directory, run the body, and remove the directory
     * even when the body throws (setup, assertion, or startup failures).
     *
     * @param callable(string): mixed $body receives the data directory
     */
    function gatewayPlannerWithDisposableDirectory(callable $body): mixed
    {
        $directory = gatewayPlannerDisposableDataDirectory();
        try {
            return $body($directory);
        } finally {
            gatewayPlannerRemoveDataDirectory($directory);
        }
    }
}

if (! function_exists('gatewayPlannerBaseEnv')) {
    /**
     * The environment every Workerman Planner host needs for a disposable
     * database. Inherited database and cache-path settings are neutralised first;
     * add server-specific variables via `gatewayPlannerWithEnvironment`.
     *
     * @return array<string, string|null>
     */
    function gatewayPlannerBaseEnv(string $dataDirectory, string $database, string $dbConnection = 'sqlite'): array
    {
        return array_merge(gatewayPlannerIsolatedEnvironment($dataDirectory), [
            'APP_KEY' => 'base64:'.base64_encode(random_bytes(32)),
            'SESSION_DRIVER' => 'array',
            'GATEWAY_INTERNAL_TOKEN' => 'gateway-internal-test-token',
            'REGISTRY_TOKEN' => 'registry-test-token',
            'DB_CONNECTION' => $dbConnection,
            'DB_DATABASE' => $database,
            'GATEWAY_ROUTER_DATA_DIR' => $dataDirectory,
            'LARAVEL_STORAGE_PATH' => $dataDirectory.'/storage',
            'APP_RUNNING_IN_CONSOLE' => 'false',
            'GATEWAY_PATH_RESOLVER' => 'Webong\\Gateway\\Tests\\Fixtures\\InstrumentedHttpSmokePathResolver',
            'GATEWAY_TEST_PROBE_DIR' => $dataDirectory.'/probes',
        ]);
    }
}

// ---------------------------------------------------------------------------
// Resolved-database verification
// ---------------------------------------------------------------------------

if (! function_exists('gatewayPlannerPostgresServerIdentity')) {
    /**
     * Identify the PostgreSQL server and database a client reaches, from outside
     * Laravel: current database, listening port, and postmaster start time (stable
     * for one server process, different across servers).
     *
     * @return array{database: string, port: int|null, started: string}
     */
    function gatewayPlannerPostgresServerIdentity(string $host, int $port, string $database, string $username, string $password): array
    {
        $pdo = new PDO(sprintf('pgsql:host=%s;port=%d;dbname=%s', $host, $port, $database), $username, $password);
        $row = $pdo->query(
            'SELECT current_database() AS database, inet_server_port() AS port, pg_postmaster_start_time()::text AS started',
        )->fetch(PDO::FETCH_ASSOC);

        return [
            'database' => (string) $row['database'],
            'port' => $row['port'] === null ? null : (int) $row['port'],
            'started' => (string) $row['started'],
        ];
    }
}

if (! function_exists('gatewayPlannerMysqlServerIdentity')) {
    /**
     * Identify the MySQL server and database a client reaches, from outside
     * Laravel: current database, listening port, and the server's UUID (stable for
     * one data directory, different across servers).
     *
     * @return array{database: string, port: int, server_uuid: string}
     */
    function gatewayPlannerMysqlServerIdentity(string $host, int $port, string $database, string $username, string $password): array
    {
        $pdo = new PDO(sprintf('mysql:host=%s;port=%d;dbname=%s', $host, $port, $database), $username, $password);
        $row = $pdo->query('SELECT DATABASE() AS name, @@port AS port, @@server_uuid AS uuid')->fetch(PDO::FETCH_ASSOC);

        return [
            'database' => (string) $row['name'],
            'port' => (int) $row['port'],
            'server_uuid' => (string) $row['uuid'],
        ];
    }
}

if (! function_exists('gatewayPlannerResolveDatabase')) {
    /**
     * Ask a fresh PHP process (booted the same way as migrations, seeding and the
     * host, under the current environment) which database Laravel resolves.
     * Performs only reads.
     *
     * @return array<string, mixed>
     */
    function gatewayPlannerResolveDatabase(): array
    {
        $script = gatewayPlannerRepositoryRoot().'/tests/Support/resolve-planner-database.php';
        $output = [];
        $exitCode = 0;
        exec(escapeshellarg(PHP_BINARY).' '.escapeshellarg($script).' 2>&1', $output, $exitCode);

        $report = null;
        foreach (array_reverse($output) as $line) {
            if (str_starts_with(ltrim($line), '{')) {
                $report = json_decode($line, true);
                break;
            }
        }
        if (! is_array($report)) {
            throw new RuntimeException("Unable to determine the resolved database (exit $exitCode):\n".implode("\n", $output));
        }

        return $report;
    }
}

if (! function_exists('gatewayPlannerAssertResolvedDatabase')) {
    /**
     * Verify the database Laravel resolves under the current environment is the
     * intended disposable one, before anything writes to it.
     *
     * The intended database comes from the same variables the host reads
     * (DB_CONNECTION, DB_DATABASE, and for PostgreSQL DB_HOST/DB_PORT/credentials).
     * SQLite: the resolved file must be the intended file. PostgreSQL: the resolved
     * database name, host and port must match, and the server reached through
     * Laravel must be the same server process the intended connection reaches.
     * In every case the configuration must not come from a cache file.
     *
     * @return array<string, mixed> the verified report
     */
    function gatewayPlannerAssertResolvedDatabase(): array
    {
        $driver = getenv('DB_CONNECTION') ?: 'sqlite';
        $intended = (string) getenv('DB_DATABASE');
        $report = gatewayPlannerResolveDatabase();
        $problems = [];

        if (($report['driver'] ?? null) !== $driver) {
            $problems[] = sprintf('driver %s, expected %s', var_export($report['driver'] ?? null, true), $driver);
        }
        if (($report['config_cached'] ?? true) !== false) {
            $problems[] = 'configuration was loaded from a cache file ('.($report['config_cache_path'] ?? '?').')';
        }
        if (($report['config_cache_path'] ?? null) !== getenv('APP_CONFIG_CACHE')) {
            $problems[] = sprintf(
                'config cache path %s is not the isolated %s',
                var_export($report['config_cache_path'] ?? null, true),
                var_export(getenv('APP_CONFIG_CACHE'), true),
            );
        }

        if ($driver === 'sqlite') {
            $resolvedFile = (string) ($report['file'] ?? '');
            if ($resolvedFile === '' || realpath($resolvedFile) !== realpath($intended)) {
                $problems[] = sprintf('SQLite file %s, expected %s', var_export($resolvedFile, true), $intended);
            }
        } elseif ($driver === 'pgsql') {
            $host = (string) getenv('DB_HOST');
            $port = (int) getenv('DB_PORT');
            if (($report['database'] ?? null) !== $intended) {
                $problems[] = sprintf('PostgreSQL database %s, expected %s', var_export($report['database'] ?? null, true), $intended);
            }
            if (($report['host'] ?? null) !== $host || ($report['port'] ?? null) !== $port) {
                $problems[] = sprintf('PostgreSQL endpoint %s:%s, expected %s:%d', $report['host'] ?? '?', $report['port'] ?? '?', $host, $port);
            }
            $expected = gatewayPlannerPostgresServerIdentity(
                $host,
                $port,
                $intended,
                (string) getenv('DB_USERNAME'),
                (string) getenv('DB_PASSWORD'),
            );
            $resolved = $report['identity'] ?? null;
            if ($resolved !== $expected) {
                $problems[] = 'PostgreSQL server identity '.json_encode($resolved).', expected '.json_encode($expected);
            }
        } elseif ($driver === 'mysql') {
            $host = (string) getenv('DB_HOST');
            $port = (int) getenv('DB_PORT');
            if (($report['database'] ?? null) !== $intended) {
                $problems[] = sprintf('MySQL database %s, expected %s', var_export($report['database'] ?? null, true), $intended);
            }
            if (($report['host'] ?? null) !== $host || ($report['port'] ?? null) !== $port) {
                $problems[] = sprintf('MySQL endpoint %s:%s, expected %s:%d', $report['host'] ?? '?', $report['port'] ?? '?', $host, $port);
            }
            $expected = gatewayPlannerMysqlServerIdentity(
                $host,
                $port,
                $intended,
                (string) getenv('DB_USERNAME'),
                (string) getenv('DB_PASSWORD'),
            );
            $resolved = $report['identity'] ?? null;
            if ($resolved !== $expected) {
                $problems[] = 'MySQL server identity '.json_encode($resolved).', expected '.json_encode($expected);
            }
        } else {
            $problems[] = "unsupported driver [$driver] for resolved-database verification";
        }

        if ($problems !== []) {
            throw new RuntimeException(
                'Refusing to write: Laravel does not resolve the intended disposable database ('
                .implode('; ', $problems).').',
            );
        }

        return $report;
    }
}

// ---------------------------------------------------------------------------
// Migrations + seeding
// ---------------------------------------------------------------------------

if (! function_exists('gatewayPlannerRunArtisanMigrations')) {
    /**
     * Run the proxy and application migrations through the bundled Laravel host
     * against whatever database the current process environment selects.
     */
    function gatewayPlannerRunArtisanMigrations(): void
    {
        $root = gatewayPlannerRepositoryRoot();
        $artisan = $root.'/app/planner/artisan';
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

if (! function_exists('gatewayPlannerRunMigrations')) {
    /**
     * Run every Planner + proxy + application migration against a disposable
     * SQLite database file.
     */
    function gatewayPlannerRunMigrations(string $database): void
    {
        if (realpath($database) === false || realpath($database) !== realpath((string) getenv('DB_DATABASE'))) {
            throw new RuntimeException(sprintf(
                'Refusing to migrate [%s]: the environment selects [%s].',
                $database,
                (string) getenv('DB_DATABASE'),
            ));
        }
        gatewayPlannerAssertResolvedDatabase();

        $root = gatewayPlannerRepositoryRoot();

        $runner = Webong\Gateway\Migrations\GatewayMigrationRunner::sqlite(
            database: $database,
            migrationsPath: $root.'/pkg/planner/migrations',
            tables: [],
        );
        $runner->migrate();

        gatewayPlannerRunArtisanMigrations();
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
        if ((string) getenv('DB_DATABASE') !== $connection['database']) {
            throw new RuntimeException(sprintf(
                'Refusing to migrate [%s]: the environment selects [%s].',
                $connection['database'],
                (string) getenv('DB_DATABASE'),
            ));
        }
        gatewayPlannerAssertResolvedDatabase();

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

        gatewayPlannerRunArtisanMigrations();
    }
}

if (! function_exists('gatewayPlannerBaseEnvPostgres')) {
    /**
     * The environment a Workerman Planner host needs for a disposable PostgreSQL
     * database.
     *
     * @param array{host: string, port: int, database: string, username: string, password: string} $connection
     * @return array<string, string|null>
     */
    function gatewayPlannerBaseEnvPostgres(string $dataDirectory, array $connection): array
    {
        return array_merge(gatewayPlannerBaseEnv($dataDirectory, $connection['database'], 'pgsql'), [
            'DB_HOST' => $connection['host'],
            'DB_PORT' => (string) $connection['port'],
            'DB_USERNAME' => $connection['username'],
            'DB_PASSWORD' => $connection['password'],
        ]);
    }
}

if (! function_exists('gatewayPlannerMysqlConnection')) {
    /**
     * Read the disposable MySQL server from the environment, or return null when
     * MySQL is not configured (tests should then skip).
     *
     * @return array{driver: string, host: string, port: int, database: string, username: string, password: string}|null
     */
    function gatewayPlannerMysqlConnection(): ?array
    {
        $database = getenv('GATEWAY_TEST_MYSQL_DATABASE');
        if (! is_string($database) || $database === '') {
            return null;
        }

        return [
            'driver' => 'mysql',
            'host' => getenv('GATEWAY_TEST_MYSQL_HOST') ?: '127.0.0.1',
            'port' => (int) (getenv('GATEWAY_TEST_MYSQL_PORT') ?: 3306),
            'database' => $database,
            'username' => getenv('GATEWAY_TEST_MYSQL_USERNAME') ?: 'root',
            'password' => getenv('GATEWAY_TEST_MYSQL_PASSWORD') ?: '',
        ];
    }
}

if (! function_exists('gatewayPlannerRunMigrationsMysql')) {
    /**
     * Run every Planner + proxy + application migration against a disposable
     * MySQL database, the way scripts/planner-start does: `gateway:migrate` through
     * the real `mysql` connection in app/planner/config.php, then Proxy's and the
     * application's migrations.
     *
     * @param array{host: string, port: int, database: string, username: string, password: string} $connection
     */
    function gatewayPlannerRunMigrationsMysql(array $connection): void
    {
        if ((string) getenv('DB_DATABASE') !== $connection['database']) {
            throw new RuntimeException(sprintf(
                'Refusing to migrate [%s]: the environment selects [%s].',
                $connection['database'],
                (string) getenv('DB_DATABASE'),
            ));
        }
        gatewayPlannerAssertResolvedDatabase();

        $artisan = gatewayPlannerRepositoryRoot().'/app/planner/artisan';
        $exitCode = null;
        $output = null;
        // The host environment says it is not a console; `gateway:migrate` is only registered in one.
        exec("APP_RUNNING_IN_CONSOLE=true php $artisan gateway:migrate --no-ansi 2>&1", $output, $exitCode);
        if ($exitCode !== 0) {
            throw new RuntimeException("gateway:migrate failed (exit $exitCode):\n".implode("\n", $output));
        }

        gatewayPlannerRunArtisanMigrations();
    }
}

if (! function_exists('gatewayPlannerBaseEnvMysql')) {
    /**
     * The environment a Workerman Planner host needs for a disposable MySQL database.
     *
     * @param array{host: string, port: int, database: string, username: string, password: string} $connection
     * @return array<string, string|null>
     */
    function gatewayPlannerBaseEnvMysql(string $dataDirectory, array $connection): array
    {
        return array_merge(gatewayPlannerBaseEnv($dataDirectory, $connection['database'], 'mysql'), [
            'DB_HOST' => $connection['host'],
            'DB_PORT' => (string) $connection['port'],
            'DB_USERNAME' => $connection['username'],
            'DB_PASSWORD' => $connection['password'],
        ]);
    }
}

if (! function_exists('gatewayPlannerSeedEndpoint')) {
    /**
     * Seed a managed endpoint + relay subscription into the configured database
     * by booting the real application (reuses EnsureEndpoint/SubscribeEndpoint
     * so the metadata format is exactly what production uses). The resolved
     * database is verified before the seed script runs, and the script verifies it
     * again before its first write.
     *
     * @param array<string, string|null> $env the environment to seed under (must include
     *                                        DB_* and the app/cache paths).
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
            gatewayPlannerAssertResolvedDatabase();

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

if (! class_exists('GatewayPlannerHostHandle')) {
    /**
     * The exact Workerman master process a helper launched. Once `closed`, the
     * handle no longer authorises any signal: the saved numeric PID may by then
     * belong to an unrelated process.
     */
    final class GatewayPlannerHostHandle
    {
        /** @var array{exitCode: int, signaled: bool, termsig: int}|null recorded the first time the process is seen to have exited */
        public ?array $exit = null;

        public bool $closed = false;

        /** @var array{forced: bool, signal: string|null, exit: array{exitCode: int, signaled: bool, termsig: int}|null}|null */
        public ?array $result = null;

        /** @param resource|null $process */
        public function __construct(
            public $process,
            public readonly int $masterPid,
            public readonly string $logFile,
        ) {}
    }
}

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

if (! function_exists('gatewayPlannerHostRegistry')) {
    /**
     * Hosts started and not yet stopped. A shutdown function force-kills whatever
     * is still registered, so a test that dies before its `finally` runs cannot
     * leave an owned host behind. Nothing outside the registry is ever touched.
     *
     * @return array<int, GatewayPlannerHostHandle>
     */
    function &gatewayPlannerHostRegistry(): array
    {
        static $registry = [];
        static $installed = false;

        if (! $installed) {
            $installed = true;
            register_shutdown_function(static function () use (&$registry): void {
                foreach ($registry as $handle) {
                    gatewayPlannerEmergencyKill($handle);
                }
            });
        }

        return $registry;
    }
}

if (! function_exists('gatewayPlannerEmergencyKill')) {
    /** Bounded last resort: SIGKILL an owned, still-open host and its workers. */
    function gatewayPlannerEmergencyKill(GatewayPlannerHostHandle $handle): void
    {
        if ($handle->closed || ! is_resource($handle->process)) {
            return;
        }
        $status = proc_get_status($handle->process);
        if ($status !== false && $status['running']) {
            foreach (gatewayPlannerWorkerPids($handle->masterPid) as $pid) {
                @posix_kill($pid, SIGKILL);
            }
            @proc_terminate($handle->process, SIGKILL);
        }
        @proc_close($handle->process);
        $handle->process = null;
        $handle->closed = true;
    }
}

if (! function_exists('gatewayPlannerStartWorkerman')) {
    /**
     * Start `app/servers/workerman/server.php` as a background process under the current
     * (already isolated) environment, after verifying which database it will use.
     *
     * @param array<string, string> $extraEnv server variables (workers, max requests, test hooks).
     */
    function gatewayPlannerStartWorkerman(string $dataDirectory, int $port, array $extraEnv = []): GatewayPlannerHostHandle
    {
        gatewayPlannerAssertResolvedDatabase();

        $root = gatewayPlannerRepositoryRoot();
        $environment = array_merge(getenv() ?: [], $extraEnv, [
            'GATEWAY_PLANNER_PORT' => (string) $port,
            'GATEWAY_PLANNER_HOST' => '127.0.0.1',
            'APP_RUNNING_IN_CONSOLE' => 'false',
            // The host is a separate process that inherits this one's environment, and phpunit.xml
            // sets APP_ENV=testing. Under that value Laravel registers an exception collector meant
            // for unit tests (Illuminate\Testing\LoggedExceptionCollection) that keeps every logged
            // exception, and with it the trace arguments, for the life of the process. A request that
            // throws then pins its Workerman connection, so a graceful stop never completes. The
            // launchers and the Planner image run the host as production, so test it as production.
            'APP_ENV' => 'production',
        ]);

        $logFile = $dataDirectory.'/storage/logs/gateway-planner-test.log';
        $descriptors = [
            0 => ['file', '/dev/null', 'r'],
            1 => ['file', $logFile, 'a'],
            2 => ['file', $logFile, 'a'],
        ];

        $process = proc_open(
            [PHP_BINARY, "{$root}/app/servers/workerman/server.php", 'start'],
            $descriptors,
            $pipes,
            $root,
            $environment,
        );
        if (! is_resource($process)) {
            throw new RuntimeException('Workerman failed to start: proc_open returned no process.');
        }
        foreach ($pipes as $pipe) {
            @fclose($pipe);
        }

        $status = proc_get_status($process);
        if ($status === false || ! isset($status['pid']) || ! ctype_digit((string) $status['pid'])) {
            @proc_terminate($process, SIGKILL);
            @proc_close($process);
            throw new RuntimeException('Workerman failed to start: unable to determine master PID.');
        }

        $handle = new GatewayPlannerHostHandle($process, (int) $status['pid'], $logFile);
        $registry = &gatewayPlannerHostRegistry();
        $registry[spl_object_id($handle)] = $handle;

        return $handle;
    }
}

if (! function_exists('gatewayPlannerPollHost')) {
    /**
     * Whether the owned master is still running. The exit status is recorded the
     * first time the process is seen to have exited (PHP reports it only once).
     */
    function gatewayPlannerPollHost(GatewayPlannerHostHandle $handle): bool
    {
        if ($handle->closed || $handle->exit !== null || ! is_resource($handle->process)) {
            return false;
        }
        $status = proc_get_status($handle->process);
        if ($status === false) {
            return false;
        }
        if ($status['running']) {
            return true;
        }
        $handle->exit = [
            'exitCode' => (int) $status['exitcode'],
            'signaled' => (bool) $status['signaled'],
            'termsig' => (int) $status['termsig'],
        ];

        return false;
    }
}

if (! function_exists('gatewayPlannerWaitForExit')) {
    /** Bounded wait for the owned master to exit by itself. */
    function gatewayPlannerWaitForExit(GatewayPlannerHostHandle $handle, float $timeoutSeconds): bool
    {
        $deadline = microtime(true) + $timeoutSeconds;
        while (gatewayPlannerPollHost($handle)) {
            if (microtime(true) >= $deadline) {
                return false;
            }
            usleep(25_000);
        }

        return true;
    }
}

if (! function_exists('gatewayPlannerSignalHost')) {
    /**
     * Send a signal to the owned master, only while the handle is open and the
     * process has not been seen to exit. Returns whether a signal was sent.
     */
    function gatewayPlannerSignalHost(GatewayPlannerHostHandle $handle, int $signal): bool
    {
        if (! gatewayPlannerPollHost($handle)) {
            return false;
        }

        return proc_terminate($handle->process, $signal);
    }
}

if (! function_exists('gatewayPlannerStopWorkerman')) {
    /**
     * Stop a Workerman host we own and reap it. Idempotent: once the handle is
     * closed this returns the recorded result and never signals anything.
     *
     * Workerman drains in-flight requests on SIGQUIT; SIGTERM/SIGINT stop it
     * immediately. The stop therefore sends SIGQUIT and waits for the master to
     * exit on its own. Only if it has not exited within the bound is it forced
     * (SIGKILL to the owned workers and master), and the result records that
     * (`forced`) so tests can fail on it.
     *
     * @return array{forced: bool, signal: string|null, exit: array{exitCode: int, signaled: bool, termsig: int}|null}
     */
    function gatewayPlannerStopWorkerman(GatewayPlannerHostHandle $handle, float $gracefulTimeoutSeconds = 15.0): array
    {
        if ($handle->closed) {
            return $handle->result ?? ['forced' => false, 'signal' => null, 'exit' => null];
        }

        $forced = false;
        $signal = null;
        try {
            if (gatewayPlannerPollHost($handle)) {
                $signal = 'SIGQUIT';
                proc_terminate($handle->process, SIGQUIT);
                if (! gatewayPlannerWaitForExit($handle, $gracefulTimeoutSeconds)) {
                    $forced = true;
                    $signal = 'SIGKILL';
                    foreach (gatewayPlannerWorkerPids($handle->masterPid) as $pid) {
                        @posix_kill($pid, SIGKILL);
                    }
                    proc_terminate($handle->process, SIGKILL);
                    gatewayPlannerWaitForExit($handle, 5.0);
                }
            }
        } finally {
            if (is_resource($handle->process)) {
                @proc_close($handle->process);
            }
            $handle->process = null;
            $handle->closed = true;
            $registry = &gatewayPlannerHostRegistry();
            unset($registry[spl_object_id($handle)]);
        }

        return $handle->result = ['forced' => $forced, 'signal' => $signal, 'exit' => $handle->exit];
    }
}

// ---------------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------------

if (! function_exists('gatewayPlannerConnect')) {
    /**
     * Open a TCP connection to the host and return the socket.
     *
     * @return resource
     */
    function gatewayPlannerConnect(string $host, int $port, float $timeoutSeconds = 5.0)
    {
        $socket = @stream_socket_client('tcp://'.$host.':'.$port, $errno, $errstr, $timeoutSeconds);
        if ($socket === false) {
            throw new RuntimeException("Unable to connect to $host:$port: $errstr ($errno)");
        }
        $seconds = (int) $timeoutSeconds;
        stream_set_timeout($socket, $seconds, (int) (($timeoutSeconds - $seconds) * 1_000_000));

        return $socket;
    }
}

if (! function_exists('gatewayPlannerSendRequest')) {
    /**
     * Write one raw HTTP/1.1 request to an open socket. A keep-alive request omits
     * `Connection: close`, so the same socket can carry the next request.
     *
     * @param resource $socket
     * @param array<string, string> $headers
     */
    function gatewayPlannerSendRequest(
        $socket,
        string $method,
        string $uri,
        array $headers = [],
        string $body = '',
        bool $keepAlive = false,
    ): void {
        $parsed = parse_url($uri);
        $host = $parsed['host'] ?? '127.0.0.1';
        $path = ($parsed['path'] ?? '/') ?: '/';
        if (! empty($parsed['query'])) {
            $path .= '?'.$parsed['query'];
        }

        $request = "$method $path HTTP/1.1\r\n";
        $request .= "Host: $host\r\n";
        if (! $keepAlive) {
            $request .= "Connection: close\r\n";
        }
        foreach ($headers as $name => $value) {
            $request .= "$name: $value\r\n";
        }
        if ($body !== '') {
            $request .= 'Content-Length: '.strlen($body)."\r\n";
        }
        $request .= "\r\n".$body;
        fwrite($socket, $request);
    }
}

if (! function_exists('gatewayPlannerOpenRequest')) {
    /**
     * Connect, send a raw HTTP/1.1 request, and return the open socket without
     * reading the response (so a test can act while the request is in flight).
     * Pass `$keepAlive` to leave the connection reusable for further requests.
     *
     * @param array<string, string> $headers
     * @return resource
     */
    function gatewayPlannerOpenRequest(
        string $method,
        string $uri,
        array $headers = [],
        string $body = '',
        float $timeoutSeconds = 5.0,
        bool $keepAlive = false,
    ) {
        $parsed = parse_url($uri);
        $socket = gatewayPlannerConnect($parsed['host'] ?? '127.0.0.1', (int) ($parsed['port'] ?? 80), $timeoutSeconds);
        gatewayPlannerSendRequest($socket, $method, $uri, $headers, $body, $keepAlive);

        return $socket;
    }
}

if (! function_exists('gatewayPlannerPeerClosed')) {
    /**
     * Whether the server has already closed its end of an idle connection, which is
     * what a keep-alive client (curl, for one) checks before it reuses a connection.
     *
     * @param resource $socket
     */
    function gatewayPlannerPeerClosed($socket): bool
    {
        $read = [$socket];
        $write = $except = null;
        if (stream_select($read, $write, $except, 0, 0) < 1) {
            return false;
        }
        $peek = @stream_socket_recvfrom($socket, 1, STREAM_PEEK);

        return $peek === '' || $peek === false;
    }
}

if (! function_exists('gatewayPlannerSocketReadable')) {
    /** @param resource $socket */
    function gatewayPlannerSocketReadable($socket, float $seconds): bool
    {
        $read = [$socket];
        $write = $except = null;

        return stream_select($read, $write, $except, (int) $seconds, (int) (($seconds - (int) $seconds) * 1_000_000)) > 0;
    }
}

if (! function_exists('gatewayPlannerReadResponse')) {
    /**
     * Read one complete HTTP response (headers plus Content-Length body) from a
     * socket, and close the socket unless told to keep it (a keep-alive client).
     *
     * @param resource $socket
     * @return array{status: int, body: string, headers: array<string, string>, raw: string, complete: bool}
     */
    function gatewayPlannerReadResponse($socket, bool $close = true): array
    {
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
        $headers = [];
        foreach (explode("\r\n", $head) as $headerLine) {
            if (preg_match('/^([^:]+):\s*(.*)$/', $headerLine, $match)) {
                $headers[strtolower($match[1])] = $match[2];
            }
        }
        $contentLength = (int) ($headers['content-length'] ?? 0);
        $body = $headEnd === false ? '' : substr($response, $headEnd + 4);
        $raw = $response;
        while ($contentLength > 0 && strlen($body) < $contentLength) {
            $chunk = @fread($socket, min(8192, $contentLength - strlen($body)));
            if ($chunk === false || $chunk === '') {
                break;
            }
            $body .= $chunk;
            $raw .= $chunk;
        }
        if ($close) {
            fclose($socket);
        }

        $status = 0;
        if (preg_match('#HTTP/1\.[01]\s+([0-9]{3})#', $response, $match)) {
            $status = (int) $match[1];
        }

        return [
            'status' => $status,
            'body' => $body,
            'headers' => $headers,
            'raw' => $raw,
            'complete' => $headEnd !== false && strlen($body) >= $contentLength,
        ];
    }
}

if (! function_exists('gatewayPlannerRequest')) {
    /**
     * Make a raw HTTP/1.1 request and return a structured response.
     *
     * @param array<string, string> $headers
     * @return array{status: int, body: string, headers: array<string, string>, raw: string, complete: bool, elapsed: float}
     */
    function gatewayPlannerRequest(
        string $method,
        string $uri,
        array $headers = [],
        string $body = '',
        float $timeoutSeconds = 5.0,
    ): array {
        $start = microtime(true);
        $socket = gatewayPlannerOpenRequest($method, $uri, $headers, $body, $timeoutSeconds);

        return gatewayPlannerReadResponse($socket) + ['elapsed' => microtime(true) - $start];
    }
}

if (! function_exists('gatewayPlannerReadProbe')) {
    /**
     * Read a record the test-only instrumented path resolver wrote while serving a
     * request (worker PID, database backend PID, transaction level, ...).
     *
     * @return array<string, mixed>
     */
    function gatewayPlannerReadProbe(string $dataDirectory, string $token, float $timeoutSeconds = 5.0): array
    {
        $file = $dataDirectory.'/probes/'.$token.'.json';
        $deadline = microtime(true) + $timeoutSeconds;
        while (! is_file($file) && microtime(true) < $deadline) {
            usleep(10_000);
        }
        if (! is_file($file)) {
            throw new RuntimeException("No probe record [$token] was written.");
        }

        return json_decode((string) file_get_contents($file), true, 512, JSON_THROW_ON_ERROR);
    }
}
