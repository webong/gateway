<?php

declare(strict_types=1);

use Illuminate\Foundation\Application;
use Webong\Gateway\GatewayServiceProvider;
use Webong\WebProxy\WebProxyServiceProvider;

$dataDirectory = getenv('GATEWAY_ROUTER_DATA_DIR') ?: dirname(__DIR__).'/.gateway-router-data';
$cacheDirectory = rtrim($dataDirectory, '/').'/cache';
if (! is_dir($cacheDirectory) && ! mkdir($cacheDirectory, 0700, true) && ! is_dir($cacheDirectory)) {
    throw new RuntimeException("Unable to create Router cache directory: {$cacheDirectory}");
}

foreach ([
    'APP_SERVICES_CACHE' => 'services.php',
    'APP_PACKAGES_CACHE' => 'packages.php',
    'APP_CONFIG_CACHE' => 'config.php',
    'APP_ROUTES_CACHE' => 'routes-v7.php',
    'APP_EVENTS_CACHE' => 'events.php',
] as $name => $file) {
    if (! isset($_ENV[$name]) && ! isset($_SERVER[$name]) && getenv($name) === false) {
        $_ENV[$name] = $cacheDirectory.'/'.$file;
    }
}

$app = Application::configure(basePath: __DIR__)
    ->withProviders([
        WebProxyServiceProvider::class,
        GatewayServiceProvider::class,
    ])
    ->withRouting(
        web: __DIR__.'/routes.php',
        commands: __DIR__.'/commands.php',
    )
    ->withMiddleware()
    ->withExceptions()
    ->create();

$app->useAppPath(__DIR__);
$app->useStoragePath(rtrim($dataDirectory, '/').'/storage');

return $app;
