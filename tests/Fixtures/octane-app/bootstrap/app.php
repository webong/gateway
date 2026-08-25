<?php

declare(strict_types=1);

use Illuminate\Foundation\Application;
use Webong\Gateway\Tests\Fixtures\HttpRegistrySmokeServiceProvider;
use Webong\Gateway\GatewayServiceProvider;

return Application::configure(basePath: dirname(__DIR__))
    ->withProviders([
        GatewayServiceProvider::class,
        HttpRegistrySmokeServiceProvider::class,
    ])
    ->withMiddleware()
    ->withRouting(
        web: dirname(__DIR__).'/routes/web.php',
    )
    ->withExceptions()
    ->create();
