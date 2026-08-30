<?php

declare(strict_types=1);

namespace Webong\Gateway\Reverb;

use Illuminate\Support\ServiceProvider;
use Laravel\Reverb\ApplicationManager;
use Webong\Gateway\Reverb\ServerTypes\ReverbServerType;
use Webong\Gateway\Servers\ServerTypeRegistry;

final class ReverbExtensionServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $this->mergeConfigFrom(__DIR__.'/../config/reverb.php', 'gateway-reverb');

        if (is_string(config('gateway-reverb.server_id')) && config('gateway-reverb.server_id') !== '') {
            config()->set('reverb.apps.provider', 'gateway');
        }

        $this->app->singleton(ReverbServerContext::class);
        $this->app->singleton(GatewayApplicationProvider::class);
        $this->app->singleton(ReverbServerType::class);
        $this->app->afterResolving(ServerTypeRegistry::class, function (ServerTypeRegistry $types): void {
            $types->register($this->app->make(ReverbServerType::class));
        });
        $this->app->afterResolving(ApplicationManager::class, function (ApplicationManager $manager): void {
            $manager->extend(
                'gateway',
                fn () => $this->app->make(GatewayApplicationProvider::class),
            );
        });
    }

    public function boot(): void
    {
        $this->publishes([
            __DIR__.'/../config/reverb.php' => config_path('gateway-reverb.php'),
        ], 'gateway-reverb-config');
    }
}
