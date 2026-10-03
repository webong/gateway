<?php

declare(strict_types=1);

use Illuminate\Foundation\Application;
use Illuminate\Foundation\Configuration\Middleware;
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
    ->withMiddleware(function (Middleware $middleware): void {
        if (getenv('GATEWAY_EMAIL_SMOKE') === '1') {
            // This isolated fixture only accepts traffic from its private
            // Spinner service; the public TLS edge supplies the scheme.
            $middleware->trustProxies(at: '*');
        }
    })
    ->withRouting(
        web: dirname(__DIR__).'/routes/web.php',
    )
    ->withExceptions()
    ->create();
