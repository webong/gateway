<?php

declare(strict_types=1);

namespace Webong\Gateway\Web;

use Illuminate\Support\ServiceProvider;
use Webong\Gateway\Automation\CapabilityAwareRoutePlanner;
use Webong\Gateway\Events\WebEventRecorder;
use Webong\Gateway\Planning\ProtocolPlannerRegistry;
use Webong\Gateway\Planning\RoutePlanObserverRegistry;
use Webong\Gateway\Protocols\Protocol;
use Webong\Gateway\Resources\ConfigurationKindRegistry;
use Webong\Gateway\Webstream\WebstreamPrivateSourceStore;
use Webong\Gateway\Webstream\WebstreamRoutePolicyStore;

final class WebServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $this->mergeConfigFrom(__DIR__.'/../config/web.php', 'gateway.events');

        $this->app->bind(WebProtocolPlanner::class, fn (): WebProtocolPlanner => new WebProtocolPlanner(
            $this->app->make(CapabilityAwareRoutePlanner::class),
            $this->app->make(WebstreamPrivateSourceStore::class),
            $this->app->make(WebstreamRoutePolicyStore::class),
        ));

        $this->app->afterResolving(ConfigurationKindRegistry::class,
            static fn (ConfigurationKindRegistry $kinds) => $kinds->register(
                'web.event-policy', 'webstream.private-source', 'webstream.route-policy',
            ));
        $this->app->afterResolving(ProtocolPlannerRegistry::class, function (ProtocolPlannerRegistry $planners): void {
            $factory = fn (): WebProtocolPlanner => $this->app->make(WebProtocolPlanner::class);
            $planners->register(Protocol::WEBSOCKET, $factory);
            $planners->register(Protocol::WEBSTREAM, $factory);
        });
        $this->app->afterResolving(RoutePlanObserverRegistry::class, function (RoutePlanObserverRegistry $observers): void {
            $observers->register('web', fn ($request, $plan) => $this->app->make(WebEventRecorder::class)->record($request, $plan));
        });
    }

    public function boot(): void
    {
        $this->loadRoutesFrom(__DIR__.'/../routes/planner.php');

        $this->publishes([__DIR__.'/../config/web.php' => config_path('gateway-web.php')], 'gateway-web-config');
    }
}
