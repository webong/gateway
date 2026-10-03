<?php

declare(strict_types=1);

return [
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
            'gateway' => Webong\Gateway\GatewayRouter::class,
        ],
    ],
];
