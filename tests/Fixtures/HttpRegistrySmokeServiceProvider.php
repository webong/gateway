<?php

declare(strict_types=1);

namespace Webong\Gateway\Tests\Fixtures;

use Illuminate\Database\Migrations\Migrator;
use Illuminate\Support\ServiceProvider;
use ReflectionClass;
use Webong\WebProxy\WebProxyServiceProvider;
use Webong\Gateway\Tests\Support\TestRouter;

final class HttpRegistrySmokeServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $database = getenv('DB_DATABASE');
        if (! is_string($database) || $database === '') {
            return;
        }

        config()->set([
            'database.default' => 'sqlite',
            'database.connections.sqlite' => [
                'driver' => 'sqlite',
                'database' => $database,
                'prefix' => '',
                'foreign_key_constraints' => true,
            ],
            'web-proxy.base_url' => getenv('WEB_PROXY_URL') ?: 'http://127.0.0.1',
            'web-proxy.routers.http-smoke' => TestRouter::class,
            'gateway.cache.enabled' => false,
            'gateway.mutations.lock_store' => 'array',
        ]);
    }

    public function boot(): void
    {
        if (getenv('GATEWAY_HTTP_REGISTRY_SMOKE') !== '1') {
            return;
        }

        /** @var Migrator $migrator */
        $migrator = $this->app->make('migrator');
        if (! $migrator->repositoryExists()) {
            $migrator->getRepository()->createRepository();
        }

        $provider = new ReflectionClass(WebProxyServiceProvider::class);
        $migrator->run(dirname((string) $provider->getFileName(), 2).'/database/migrations');
    }
}
