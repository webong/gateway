<?php

declare(strict_types=1);

return [
    'client' => (string) env('GATEWAY_DNS_PROXY_CLIENT', 'gateway'),
    'providers' => [
        'cloudflare' => [
            'token' => (string) env('GATEWAY_DNS_CLOUDFLARE_TOKEN', ''),
            'base_url' => (string) env('GATEWAY_DNS_CLOUDFLARE_BASE_URL', 'https://api.cloudflare.com/client/v4'),
        ],
    ],
];
