<?php

declare(strict_types=1);

namespace Webong\Gateway\Shell;

use Illuminate\Support\ServiceProvider;
use Webong\Gateway\Contracts\AutomationCapability;
use Webong\Gateway\Contracts\AutomationExecutionTarget;
use Webong\Gateway\Contracts\AutomationLanguageRegistry;
use Webong\Gateway\ExtensionSecretProviderRegistry;
use Webong\Gateway\Laravel\SubscriptionTargetExtensionRegistry;
use Webong\Gateway\Resources\ConfigurationKindRegistry;
use Webong\Gateway\Shell\Http\ShellHookGrantController;
use Webong\Gateway\Shell\Http\ShellRunController;
use Webong\Gateway\Shell\Http\ShellSshSubscriptionController;

final class ShellExtensionServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $this->mergeConfigFrom(dirname(__DIR__, 2).'/config/shell.php', 'gateway.shell');
        $this->app->singleton(ShellOperatorAuthenticator::class);
        $this->app->singleton(ShellSubscriptionExtension::class);
    }

    public function boot(): void
    {
        $this->app->make(AutomationLanguageRegistry::class)->register(
            ShellLanguage::ID,
            AutomationExecutionTarget::SPINNER,
            AutomationCapability::ROUTE,
        );
        $this->app->make(ConfigurationKindRegistry::class)->register(
            'shell.hook-grant',
            'shell.ssh-execution',
        );
        $this->app->make(SubscriptionTargetExtensionRegistry::class)
            ->register($this->app->make(ShellSubscriptionExtension::class));
        $this->app->make(ExtensionSecretProviderRegistry::class)
            ->register($this->app->make(ShellExtensionSecretProvider::class));

        $this->app->booted(function (): void {
            $router = $this->app['router'];
            $router->get('/shell/runs/{run}', ShellRunController::class)
                ->name('shell.runs.show');
            $router->get('/shell/hook-grants', [ShellHookGrantController::class, 'index']);
            $router->post('/shell/hook-grants', [ShellHookGrantController::class, 'store']);
            $router->get('/shell/hook-grants/{grant}', [ShellHookGrantController::class, 'show']);
            $router->put('/shell/hook-grants/{grant}', [ShellHookGrantController::class, 'replace']);
            $router->delete('/shell/hook-grants/{grant}', [ShellHookGrantController::class, 'destroy']);
            $router->post(
                '/registry/endpoints/{endpointKey}/subscriptions/{destinationId}/ssh/rotate-key',
                ShellSshSubscriptionController::class,
            )->name('registry.subscriptions.ssh.rotate-key');
        });
    }
}
