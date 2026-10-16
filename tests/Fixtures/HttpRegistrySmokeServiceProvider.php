<?php

declare(strict_types=1);

namespace Webong\Gateway\Tests\Fixtures;

use Illuminate\Database\Migrations\Migrator;
use Illuminate\Support\ServiceProvider;
use ReflectionClass;
use Webong\Gateway\Email\TxtResolver;
use Webong\Gateway\GatewayServiceProvider;
use Webong\WebProxy\WebProxyServiceProvider;
use Webong\Gateway\Tests\Support\TestRouter;

final class HttpRegistrySmokeServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        if (getenv('GATEWAY_EMAIL_SMOKE') === '1') {
            $this->app->bind(TxtResolver::class, EmailSmokeTxtResolver::class);
        }
        $database = getenv('DB_DATABASE');
        if (! is_string($database) || $database === '') {
            return;
        }

        $settings = [
            'proxy.base_url' => getenv('PROXY_URL') ?: 'http://127.0.0.1',
            'proxy.routers.http-smoke' => TestRouter::class,
            'gateway.cache.enabled' => false,
            'gateway.mutations.lock_store' => 'array',
        ];
        foreach ([
            'gateway.email.upstream_allowed_hosts' => 'GATEWAY_EMAIL_UPSTREAM_ALLOWED_HOSTS',
            'gateway.email.hosted_allowed_hosts' => 'GATEWAY_EMAIL_HOSTED_ALLOWED_HOSTS',
        ] as $key => $variable) {
            $value = getenv($variable);
            if (is_string($value) && $value !== '') {
                $settings[$key] = array_values(array_filter(array_map('trim', explode(',', $value))));
            }
        }
        if (getenv('DB_CONNECTION') !== 'pgsql') {
            $settings['database.default'] = 'sqlite';
            $settings['database.connections.sqlite'] = [
                'driver' => 'sqlite',
                'database' => $database,
                'prefix' => '',
                'foreign_key_constraints' => true,
            ];
        }

        config()->set($settings);
    }

    public function boot(): void
    {
        if (getenv('GATEWAY_HTTP_REGISTRY_SMOKE') !== '1' || getenv('GATEWAY_SMOKE_SKIP_MIGRATIONS') === '1') {
            return;
        }

        /** @var Migrator $migrator */
        $migrator = $this->app->make('migrator');
        if (! $migrator->repositoryExists()) {
            $migrator->getRepository()->createRepository();
        }

        $provider = new ReflectionClass(WebProxyServiceProvider::class);
        $migrator->run(dirname((string) $provider->getFileName(), 2).'/database/migrations');

        $gatewayProvider = new ReflectionClass(GatewayServiceProvider::class);
        $migrator->run(dirname((string) $gatewayProvider->getFileName()).'/database/migrations');

    }
}
