<?php

declare(strict_types=1);

use Illuminate\Foundation\Application;
use Webong\Gateway\Tests\Fixtures\HttpRegistrySmokeServiceProvider;
use Webong\Gateway\GatewayServiceProvider;

$extensionProviders = array_values(array_filter(array_map(
    'trim',
    explode(',', getenv('GATEWAY_SMOKE_PROVIDERS') ?: ''),
)));

return Application::configure(basePath: dirname(__DIR__))
    ->withProviders([
        GatewayServiceProvider::class,
        HttpRegistrySmokeServiceProvider::class,
        ...$extensionProviders,
    ])
    ->withMiddleware()
    ->withRouting(
        web: dirname(__DIR__).'/routes/web.php',
    )
    ->withExceptions()
    ->create();
