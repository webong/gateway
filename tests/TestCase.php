<?php

declare(strict_types=1);

namespace Webong\WebRelay\Tests;

use Orchestra\Testbench\TestCase as Orchestra;
use ReflectionClass;
use Spatie\WebhookClient\WebhookClientServiceProvider;
use Spatie\WebhookServer\WebhookServerServiceProvider;
use Webong\WebProxy\WebProxyServiceProvider;
use Webong\WebRelay\WebRelayServiceProvider;

abstract class TestCase extends Orchestra
{
    /** @return list<class-string> */
    protected function getPackageProviders($app): array
    {
        return [
            WebhookClientServiceProvider::class,
            WebhookServerServiceProvider::class,
            WebProxyServiceProvider::class,
            WebRelayServiceProvider::class,
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
            'web-relay.path_resolver' => Support\TestPathResolver::class,
            'web-relay.registry_token' => 'registry-secret',
        ]);
    }

    protected function defineDatabaseMigrations(): void
    {
        $provider = new ReflectionClass(WebProxyServiceProvider::class);
        $this->loadMigrationsFrom(dirname((string) $provider->getFileName(), 2).'/database/migrations');
    }
}
