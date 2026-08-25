<?php

declare(strict_types=1);

use Illuminate\Foundation\Application;
use Webong\NetGateway\Tests\Fixtures\HttpRegistrySmokeServiceProvider;
use Webong\NetGateway\NetGatewayServiceProvider;

return Application::configure(basePath: dirname(__DIR__))
    ->withProviders([
        NetGatewayServiceProvider::class,
        HttpRegistrySmokeServiceProvider::class,
    ])
    ->withMiddleware()
    ->withRouting(
        web: dirname(__DIR__).'/routes/web.php',
    )
    ->withExceptions()
    ->create();
