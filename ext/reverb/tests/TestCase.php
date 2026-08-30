<?php

declare(strict_types=1);

namespace Webong\Gateway\Reverb\Tests;

use Laravel\Reverb\ApplicationManagerServiceProvider;
use Laravel\Reverb\ReverbServiceProvider;
use Orchestra\Testbench\TestCase as Orchestra;
use ReflectionClass;
use Webong\Gateway\GatewayServiceProvider;
use Webong\Gateway\Reverb\ReverbExtensionServiceProvider;

abstract class TestCase extends Orchestra
{
    /** @return list<class-string> */
    protected function getPackageProviders($app): array
    {
        return [
            ApplicationManagerServiceProvider::class,
            ReverbServiceProvider::class,
            GatewayServiceProvider::class,
            ReverbExtensionServiceProvider::class,
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
            'gateway.registry_token' => 'registry-secret',
            'gateway.internal_token' => 'internal-secret',
            'gateway-reverb.server_id' => null,
            'gateway.servers.tables.servers' => 'test_gateway_servers',
            'gateway.servers.tables.applications' => 'test_gateway_applications',
            'gateway.servers.tables.instances' => 'test_gateway_instances',
        ]);
    }

    protected function defineDatabaseMigrations(): void
    {
        $provider = new ReflectionClass(GatewayServiceProvider::class);
        $this->loadMigrationsFrom(dirname((string) $provider->getFileName(), 2).'/database/migrations');
    }
}
