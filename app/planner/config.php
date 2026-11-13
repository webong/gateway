<?php

declare(strict_types=1);

return [
    'octane' => [
        'server' => env('OCTANE_SERVER', 'frankenphp'),
        'roadrunner' => [
            // The Planner host keeps Composer dependencies in the shared vendor
            // directory beside app/, not inside it as a conventional Laravel
            // application would. Octane resolves this against app/planner.
            'command' => '../../vendor/bin/roadrunner-worker',
        ],
    ],
    'cache' => [
        'default' => 'file',
        'stores' => [
            'file' => [
                'driver' => 'file',
                'path' => storage_path('framework/cache/data'),
            ],
            'database' => [
                'driver' => 'database',
                'connection' => env('DB_CONNECTION', 'sqlite'),
                'table' => 'gateway_cache',
                'lock_table' => 'gateway_cache_locks',
            ],
        ],
        'prefix' => 'gateway',
    ],
    'database' => [
        'default' => env('DB_CONNECTION', 'sqlite'),
        'connections' => [
            'sqlite' => [
                'driver' => 'sqlite',
                'database' => env('DB_DATABASE', '/data/gateway.sqlite'),
                'prefix' => '',
                'foreign_key_constraints' => true,
            ],
            'pgsql' => [
                'driver' => 'pgsql',
                'url' => env('DB_URL'),
                'host' => env('DB_HOST', '127.0.0.1'),
                'port' => env('DB_PORT', '5432'),
                'database' => env('DB_DATABASE', 'gateway'),
                'username' => env('DB_USERNAME', 'gateway'),
                'password' => env('DB_PASSWORD', ''),
                'charset' => 'utf8',
                'prefix' => '',
                'prefix_indexes' => true,
                'search_path' => 'public',
                'sslmode' => env('DB_SSLMODE', 'prefer'),
            ],
            // The Planner runs on MySQL 8.4. The Router's shared delivery queue does
            // not: it needs PostgreSQL (see docs/deployment.md#database-support).
            'mysql' => [
                'driver' => 'mysql',
                'url' => env('DB_URL'),
                'host' => env('DB_HOST', '127.0.0.1'),
                'port' => env('DB_PORT', '3306'),
                'database' => env('DB_DATABASE', 'gateway'),
                'username' => env('DB_USERNAME', 'gateway'),
                'password' => env('DB_PASSWORD', ''),
                'unix_socket' => env('DB_SOCKET', ''),
                'charset' => env('DB_CHARSET', 'utf8mb4'),
                // Exact comparisons, like PostgreSQL and SQLite: `gateway:migrate` creates its
                // tables with this collation; Proxy's and the Email extension's inherit the
                // database default.
                'collation' => env('DB_COLLATION', 'utf8mb4_0900_bin'),
                // Those Laravel-style tables use TIMESTAMP, which converts through the session zone.
                'timezone' => env('DB_TIMEZONE', '+00:00'),
                // PostgreSQL's default; MySQL's REPEATABLE READ reads stale snapshots and
                // takes gap locks that Gateway's lock and upsert flows do not expect.
                'isolation_level' => env('DB_ISOLATION_LEVEL', 'READ COMMITTED'),
                'prefix' => '',
                'prefix_indexes' => true,
                'strict' => true,
                'engine' => null,
                'options' => extension_loaded('pdo_mysql') ? array_filter([
                    // PDO::MYSQL_ATTR_SSL_CA is deprecated from PHP 8.5.
                    (PHP_VERSION_ID >= 80500 ? \Pdo\Mysql::ATTR_SSL_CA : PDO::MYSQL_ATTR_SSL_CA) => env('MYSQL_ATTR_SSL_CA'),
                ]) : [],
            ],
        ],
        'migrations' => [
            'table' => 'migrations',
            'update_date_on_publish' => true,
        ],
    ],
    'proxy' => [
        'base_url' => env('GATEWAY_PUBLIC_URL', 'http://localhost:8080'),
        'defaults' => [
            'channel' => 'default',
            'registry' => 'local',
        ],
        'channels' => [
            [
                'name' => 'default',
                'driver' => 'webhook',
                'path' => '/',
                'methods' => ['GET', 'POST'],
                'client' => ['store_headers' => '*'],
                'registries' => ['local'],
            ],
        ],
        'routers' => [
            'gateway' => Webong\Gateway\Laravel\GatewayRouter::class,
        ],
    ],
];
