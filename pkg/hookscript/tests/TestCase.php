<?php

declare(strict_types=1);

namespace Webong\Gateway\HookScript\Tests;

use Orchestra\Testbench\TestCase as Orchestra;
use ReflectionClass;
use Webong\Gateway\GatewayServiceProvider;
use Webong\Gateway\HookScript\Tests\Fixtures\HookScriptPlanner;
use Webong\Gateway\HookScript\HookScriptExtensionServiceProvider;

abstract class TestCase extends Orchestra
{
    protected function getPackageProviders($app): array
    {
        return [GatewayServiceProvider::class, HookScriptExtensionServiceProvider::class];
    }

    protected function defineEnvironment($app): void
    {
        $app['config']->set([
            'app.key' => 'base64:'.base64_encode(str_repeat('w', 32)),
            'database.default' => 'sqlite',
            'database.connections.sqlite' => [
                'driver' => 'sqlite',
                'database' => ':memory:',
                'prefix' => '',
                'foreign_key_constraints' => true,
            ],
            'gateway.planner' => HookScriptPlanner::class,
        ]);
    }

    protected function defineDatabaseMigrations(): void
    {
        $provider = new ReflectionClass(GatewayServiceProvider::class);
        $this->loadMigrationsFrom(dirname((string) $provider->getFileName()).'/database/migrations');
    }
}
