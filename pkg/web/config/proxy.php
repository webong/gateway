<?php

declare(strict_types=1);

return [
    'channels' => [[
        'name' => 'default',
        'driver' => 'webhook',
        'path' => '/',
        'methods' => ['GET', 'POST'],
        'client' => ['store_headers' => '*'],
        'registries' => ['local'],
    ]],
    'routers' => [
        'gateway' => Webong\Gateway\Web\WebhookRouter::class,
    ],
];
