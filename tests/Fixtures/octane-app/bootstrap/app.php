<?php

declare(strict_types=1);

use Illuminate\Foundation\Application;
use Webong\WebRelay\Tests\Fixtures\HttpRegistrySmokeServiceProvider;
use Webong\WebRelay\WebRelayServiceProvider;

return Application::configure(basePath: dirname(__DIR__))
    ->withProviders([
        WebRelayServiceProvider::class,
        HttpRegistrySmokeServiceProvider::class,
    ])
    ->withMiddleware()
    ->withRouting(
        web: dirname(__DIR__).'/routes/web.php',
    )
    ->withExceptions()
    ->create();
