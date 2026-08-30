<?php

declare(strict_types=1);

namespace Webong\Gateway\Centrifugo\Tests;

use Orchestra\Testbench\TestCase as Orchestra;
use ReflectionClass;
use Webong\Gateway\Centrifugo\CentrifugoExtensionServiceProvider;
use Webong\Gateway\GatewayServiceProvider;

abstract class TestCase extends Orchestra
{
    protected function getPackageProviders($app): array
    {
        return [GatewayServiceProvider::class, CentrifugoExtensionServiceProvider::class];
    }

    protected function defineEnvironment($app): void
    {
        $app['config']->set([
            'app.key' => 'base64:'.base64_encode(str_repeat('c', 32)),
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
