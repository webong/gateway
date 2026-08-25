<?php

declare(strict_types=1);

return [
    'default' => 'stderr',
    'channels' => [
        'stderr' => [
            'driver' => 'monolog',
            'handler' => Monolog\Handler\StreamHandler::class,
            'with' => [
                'stream' => 'php://stderr',
            ],
        ],
    ],
];
