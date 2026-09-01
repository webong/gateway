<?php

declare(strict_types=1);

namespace Webong\Gateway\Tests;

use Orchestra\Testbench\TestCase as Orchestra;
use ReflectionClass;
use Spatie\WebhookClient\WebhookClientServiceProvider;
use Spatie\WebhookServer\WebhookServerServiceProvider;
use Webong\WebProxy\WebProxyServiceProvider;
use Webong\Gateway\GatewayServiceProvider;

abstract class TestCase extends Orchestra
{
    /** @return list<class-string> */
    protected function getPackageProviders($app): array
    {
        return [
            WebhookClientServiceProvider::class,
            WebhookServerServiceProvider::class,
            WebProxyServiceProvider::class,
            GatewayServiceProvider::class,
        ];
    }

    protected function defineEnvironment($app): void
    {
        $app['config']->set([
            'app.key' => 'base64:'.base64_encode(str_repeat('a', 32)),
            'database.default' => 'sqlite',
            'database.connections.sqlite' => [
                'driver' => 'sqlite',
                'database' => ':memory:',
                'prefix' => '',
                'foreign_key_constraints' => true,
            ],
            'cache.default' => 'array',
            'cache.stores.array' => [
                'driver' => 'array',
                'serialize' => true,
            ],
            'web-proxy.base_url' => 'https://relay.example.test',
            'web-proxy.defaults.channel' => 'default',
            'web-proxy.defaults.registry' => 'local',
            'web-proxy.channels' => [[
                'name' => 'default',
                'driver' => 'webhook',
                'path' => '/',
                'methods' => ['GET', 'POST'],
                'registries' => ['local'],
                'client' => ['store_headers' => '*'],
            ]],
            'web-proxy.registries.local' => [
                'driver' => 'database',
                'provider' => 'database',
            ],
            'web-proxy.providers.database' => [
                'driver' => 'database',
            ],
            'web-proxy.routers' => [
                'relay-test' => Support\TestRouter::class,
            ],
            'gateway.path_resolver' => Support\TestPathResolver::class,
            'gateway.registry_token' => 'registry-secret',
            'gateway.internal_token' => 'internal-secret',
            'gateway.dns.enabled' => true,
            'gateway.dns.zone' => 'dns.example.test',
            'gateway.dns.client' => 'gateway',
            'gateway.servers.tables.servers' => 'managed_servers',
            'gateway.servers.tables.applications' => 'managed_applications',
            'gateway.servers.tables.instances' => 'managed_instances',
            'gateway.cache.enabled' => true,
            'gateway.cache.store' => 'array',
            'gateway.cache.path_ttl' => 300,
            'gateway.cache.route_ttl' => 300,
            'gateway.cache.missing_ttl' => 30,
        ]);
    }

    protected function defineDatabaseMigrations(): void
    {
        $gatewayProvider = new ReflectionClass(GatewayServiceProvider::class);
        $this->loadMigrationsFrom(dirname((string) $gatewayProvider->getFileName(), 2).'/database/migrations');

        $provider = new ReflectionClass(WebProxyServiceProvider::class);
        $this->loadMigrationsFrom(dirname((string) $provider->getFileName(), 2).'/database/migrations');
    }
}
