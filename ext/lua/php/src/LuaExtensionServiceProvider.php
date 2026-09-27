<?php

declare(strict_types=1);

namespace Webong\Gateway\Lua;

use Illuminate\Support\ServiceProvider;
use Webong\Gateway\Contracts\AutomationCapability;
use Webong\Gateway\Contracts\AutomationExecutionTarget;
use Webong\Gateway\Contracts\AutomationLanguageRegistry;

final class LuaExtensionServiceProvider extends ServiceProvider
{
    public function boot(): void
    {
        $this->app->make(AutomationLanguageRegistry::class)->register(
            LuaLanguage::ID,
            AutomationExecutionTarget::SPINNER,
            AutomationCapability::ROUTE,
            AutomationCapability::SCHEDULE,
        );
    }
}
