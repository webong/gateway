<?php

declare(strict_types=1);

$config = require dirname(__DIR__).'/vendor/webong/web-proxy/config/web-proxy.php';

$config['base_url'] = env('GATEWAY_PUBLIC_URL', 'http://localhost:8080');
$config['defaults']['channel'] = 'default';
$config['defaults']['registry'] = 'local';
$config['channels'][0]['registries'] = ['local'];
$config['routers']['gateway'] = Webong\Gateway\GatewayRouter::class;

return $config;
