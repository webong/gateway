<?php

declare(strict_types=1);

use Illuminate\Foundation\Application;
use Illuminate\Foundation\Bootstrap\LoadConfiguration;
use Illuminate\Foundation\PackageManifest;
use Webong\Gateway\GatewayServiceProvider;

$vendorDirectory = dirname(__DIR__).'/vendor';
$loader = require $vendorDirectory.'/autoload.php';
$loader->addPsr4('Gateway\\Router\\', __DIR__);

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
    ->withRouting(
        web: __DIR__.'/routes.php',
        commands: __DIR__.'/commands.php',
    )
    ->withMiddleware()
    ->withExceptions()
    ->create();

$app->useAppPath(__DIR__);
$app->usePublicPath(__DIR__.'/public');
$app->useStoragePath(rtrim($dataDirectory, '/').'/storage');

// Laravel's cache commands reload bootstrap/app.php. Keep that compatibility
// entrypoint in generated state, not in the flat application source tree.
$bootstrapDirectory = rtrim($dataDirectory, '/').'/bootstrap';
if (! is_dir($bootstrapDirectory) && ! mkdir($bootstrapDirectory, 0700, true) && ! is_dir($bootstrapDirectory)) {
    throw new RuntimeException("Unable to create Router bootstrap directory: {$bootstrapDirectory}");
}
$bootstrapSource = '<?php return require '.var_export(__FILE__, true).';'.PHP_EOL;
if (@file_get_contents($bootstrapDirectory.'/app.php') !== $bootstrapSource) {
    if (file_put_contents($bootstrapDirectory.'/app.php', $bootstrapSource, LOCK_EX) === false) {
        throw new RuntimeException('Unable to write Router compatibility bootstrap.');
    }
}
$app->useBootstrapPath($bootstrapDirectory);

// Discovery uses the shared vendor directory, not a conventional app/vendor.
$app->make(PackageManifest::class)->vendorPath = $vendorDirectory;

$app->afterBootstrapping(LoadConfiguration::class, static function (Application $app) use ($vendorDirectory): void {
    if ($app->configurationIsCached()) {
        return;
    }

    foreach (require __DIR__.'/config.php' as $namespace => $values) {
        $defaults = $app['config']->get($namespace, []);
        $app['config']->set($namespace, is_array($values) && is_array($defaults)
            ? array_replace_recursive($defaults, $values)
            : $values);
    }

    $app->detectEnvironment(static fn () => $app['config']->get('app.env', 'production'));
    date_default_timezone_set($app['config']->get('app.timezone', 'UTC'));

    // In a source checkout the Planner is a path package, not a discovered
    // dependency. Installed Router distributions discover it normally.
    if (! is_file($vendorDirectory.'/webong/gateway-planner/composer.json')) {
        $app['config']->push('app.providers', GatewayServiceProvider::class);
    }
});

return $app;
