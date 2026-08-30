<?php

declare(strict_types=1);

namespace Webong\Gateway\Mercure\Tests;

use Orchestra\Testbench\TestCase as Orchestra;
use ReflectionClass;
use Webong\Gateway\GatewayServiceProvider;
use Webong\Gateway\Mercure\MercureExtensionServiceProvider;

abstract class TestCase extends Orchestra
{
    protected function getPackageProviders($app): array
    {
        return [GatewayServiceProvider::class, MercureExtensionServiceProvider::class];
    }

    protected function defineEnvironment($app): void
    {
        $app['config']->set([
            'app.key' => 'base64:'.base64_encode(str_repeat('m', 32)),
            'database.default' => 'sqlite',
            'database.connections.sqlite' => ['driver' => 'sqlite', 'database' => ':memory:', 'prefix' => '', 'foreign_key_constraints' => true],
            'gateway.registry_token' => 'registry-secret',
            'gateway.internal_token' => 'internal-secret',
        ]);
    }

    protected function defineDatabaseMigrations(): void
    {
        $provider = new ReflectionClass(GatewayServiceProvider::class);
        $this->loadMigrationsFrom(dirname((string) $provider->getFileName(), 2).'/database/migrations');
    }
}
