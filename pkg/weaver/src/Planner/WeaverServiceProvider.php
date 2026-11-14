<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver\Planner;

use Illuminate\Support\ServiceProvider;
use InvalidArgumentException;
use Webong\Gateway\Companion\ExtensionRuntimeConfigurationRegistry;
use Webong\Gateway\Resources\ConfigurationKindRegistry;

final class WeaverServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $this->app->singleton(WeaverResources::class);
        $this->app->afterResolving(ConfigurationKindRegistry::class,
            static fn(ConfigurationKindRegistry $kinds) => $kinds->register(WeaverResources::DEFINITION, WeaverResources::GRANT));
        $this->app->afterResolving(ExtensionRuntimeConfigurationRegistry::class, function(ExtensionRuntimeConfigurationRegistry $registry): void {
            $registry->register('webong/gateway-weaver', function(array $base): array {
                try {
                    return $this->app->make(WeaverResources::class)->project($base);
                } catch (InvalidArgumentException) {
                    // Send a rejectable snapshot rather than retaining a stale
                    // runtime because one resource projection returned HTTP 500.
                    return ['schema_version' => 0];
                }
            });
        });
    }

    public function boot(): void
    {
        $this->loadRoutesFrom(__DIR__.'/../../routes/planner.php');
    }
}
