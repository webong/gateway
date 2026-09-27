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
            'default' => [
                'registries' => ['local'],
            ],
        ],
        'routers' => [
            'gateway' => Webong\Gateway\GatewayRouter::class,
        ],
    ],
];
