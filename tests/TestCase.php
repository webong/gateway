<?php

declare(strict_types=1);

namespace Webong\NetGateway\Tests;

use Orchestra\Testbench\TestCase as Orchestra;
use ReflectionClass;
use Spatie\WebhookClient\WebhookClientServiceProvider;
use Spatie\WebhookServer\WebhookServerServiceProvider;
use Webong\WebProxy\WebProxyServiceProvider;
use Webong\NetGateway\NetGatewayServiceProvider;

abstract class TestCase extends Orchestra
{
    /** @return list<class-string> */
    protected function getPackageProviders($app): array
    {
        return [
            WebhookClientServiceProvider::class,
            WebhookServerServiceProvider::class,
            WebProxyServiceProvider::class,
            NetGatewayServiceProvider::class,
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
            'net-gateway.path_resolver' => Support\TestPathResolver::class,
            'net-gateway.registry_token' => 'registry-secret',
            'net-gateway.cache.enabled' => true,
            'net-gateway.cache.store' => 'array',
            'net-gateway.cache.path_ttl' => 300,
            'net-gateway.cache.route_ttl' => 300,
            'net-gateway.cache.missing_ttl' => 30,
        ]);
    }

    protected function defineDatabaseMigrations(): void
    {
        $provider = new ReflectionClass(WebProxyServiceProvider::class);
        $this->loadMigrationsFrom(dirname((string) $provider->getFileName(), 2).'/database/migrations');
    }
}
