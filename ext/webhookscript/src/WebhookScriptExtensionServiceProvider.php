<?php

declare(strict_types=1);

namespace Webong\Gateway\WebhookScript;

use Illuminate\Support\ServiceProvider;
use Webong\Gateway\Contracts\AutomationCapability;
use Webong\Gateway\Contracts\AutomationExecutionTarget;
use Webong\Gateway\Contracts\AutomationLanguageRegistry;
use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Contracts\ScriptScheduleRegistrar;
use Webong\Gateway\Email\CredentialScriptRegistry;

final class WebhookScriptExtensionServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $this->mergeConfigFrom(__DIR__.'/../config/webhookscript.php', 'gateway-webhookscript');

        $this->app->singleton(WebhookScriptRuntime::class, fn (): WebhookScriptRuntime => new WebhookScriptRuntime(
            maxSourceBytes: max(1, (int) config('gateway-webhookscript.max_source_bytes', 65536)),
            timeoutSeconds: max(1, (int) config('gateway-webhookscript.timeout_seconds', 1)),
            maxDeliveries: max(1, (int) config('gateway-webhookscript.max_deliveries', 32)),
            maxSchedules: max(1, (int) config('gateway-webhookscript.max_schedules', 8)),
            maxLogs: max(1, (int) config('gateway-webhookscript.max_logs', 100)),
        ));

        $this->app->extend(RoutePlanner::class, fn (RoutePlanner $planner): RoutePlanner => new PrimiRoutePlanner(
            planner: $planner,
            runtime: $this->app->make(WebhookScriptRuntime::class),
            schedules: $this->app->make(ScriptScheduleRegistrar::class),
        ));
    }

    public function boot(): void
    {
        $this->app->make(AutomationLanguageRegistry::class)->register(
            WebhookScriptLanguage::ID,
            AutomationExecutionTarget::PLANNER,
            AutomationCapability::ROUTE,
            AutomationCapability::CREDENTIAL,
        );
        $this->app->make(CredentialScriptRegistry::class)->register(
            WebhookScriptLanguage::ID,
            new WebhookScriptCredentialExecutor($this->app->make(WebhookScriptRuntime::class)),
        );

        $this->publishes([
            __DIR__.'/../config/webhookscript.php' => config_path('gateway-webhookscript.php'),
        ], 'gateway-webhookscript-config');
    }
}
