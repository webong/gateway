<?php

declare(strict_types=1);

return [
    'provisioners' => [
        'cloudflare' => [
            'token' => (string) env('GATEWAY_DNS_CLOUDFLARE_TOKEN', ''),
            'base_url' => 'https://api.cloudflare.com/client/v4',
        ],
    ],
];
