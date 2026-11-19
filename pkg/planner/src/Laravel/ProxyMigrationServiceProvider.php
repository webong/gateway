<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel;

use Illuminate\Support\ServiceProvider;
use Webong\Gateway\Migrations\MigrationRegistry;
use Webong\Proxy\WebProxyServiceProvider;

/** Laravel Proxy integration declares its schema independently of Web. */
final class ProxyMigrationServiceProvider extends ServiceProvider
{
    public function boot(): void
    {
        $this->app->booted(function (): void {
            if ($this->app->getProvider(WebProxyServiceProvider::class) === null) {
                return;
            }
            // Reuse the adapter's own declared migration publish group. Do not
            // infer vendor paths or opt into its separate legacy migration group.
            $paths = array_keys(ServiceProvider::pathsToPublish(WebProxyServiceProvider::class, 'proxy-migrations'));
            foreach ($paths as $index => $path) {
                $owner = 'webong/proxy-laravel'.($index === 0 ? '' : '/'.$index);
                $this->app->make(MigrationRegistry::class)->registerLaravel($owner, $path, beforeCore: true);
                $this->loadMigrationsFrom($path);
            }
        });
    }
}
