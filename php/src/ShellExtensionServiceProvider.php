<?php

declare(strict_types=1);

namespace Webong\Gateway\Shell;

use Illuminate\Support\ServiceProvider;
use Webong\Gateway\Contracts\AutomationCapability;
use Webong\Gateway\Contracts\AutomationExecutionTarget;
use Webong\Gateway\Contracts\AutomationLanguageRegistry;
use Webong\Gateway\Shell\Http\ShellRunController;

final class ShellExtensionServiceProvider extends ServiceProvider
{
    public function boot(): void
    {
        $this->app->make(AutomationLanguageRegistry::class)->register(
            ShellLanguage::ID,
            AutomationExecutionTarget::SPINNER,
            AutomationCapability::ROUTE,
        );

        $this->app->booted(function (): void {
            $this->app['router']->get('/shell/runs/{run}', ShellRunController::class)
                ->name('shell.runs.show');
        });
    }
}
