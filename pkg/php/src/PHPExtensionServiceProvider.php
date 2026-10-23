<?php

declare(strict_types=1);

namespace Webong\Gateway\PHP;

use Illuminate\Support\ServiceProvider;
use Webong\Gateway\Contracts\AutomationLanguageRegistry;
use Webong\Gateway\Contracts\AutomationCapability;
use Webong\Gateway\Contracts\AutomationExecutionTarget;

final class PHPExtensionServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $this->app->singleton(AutomationLanguageRegistry::class, function (): AutomationLanguageRegistry {
            $registry = new \Webong\Gateway\Automation\InMemoryAutomationLanguageRegistry;
            $registry->register(
                'php',
                AutomationExecutionTarget::SPINNER,
                AutomationCapability::ROUTE,
                AutomationCapability::SCHEDULE,
            );
            return $registry;
        });
    }

    public function boot(): void
    {
        //
    }
}