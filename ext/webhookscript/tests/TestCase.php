<?php

declare(strict_types=1);

namespace Webong\Gateway\WebhookScript\Tests;

use Orchestra\Testbench\TestCase as Orchestra;
use Webong\Gateway\GatewayServiceProvider;
use Webong\Gateway\WebhookScript\Tests\Fixtures\WebhookScriptPlanner;
use Webong\Gateway\WebhookScript\WebhookScriptExtensionServiceProvider;

abstract class TestCase extends Orchestra
{
    protected function getPackageProviders($app): array
    {
        return [GatewayServiceProvider::class, WebhookScriptExtensionServiceProvider::class];
    }

    protected function defineEnvironment($app): void
    {
        $app['config']->set([
            'app.key' => 'base64:'.base64_encode(str_repeat('w', 32)),
            'gateway.planner' => WebhookScriptPlanner::class,
        ]);
    }
}
