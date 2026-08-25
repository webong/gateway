<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Illuminate\Contracts\Cache\Factory as CacheFactory;
use Illuminate\Support\ServiceProvider;
use RuntimeException;
use Webong\Gateway\Contracts\PathResolver;
use Webong\Gateway\Contracts\RoutePlanner;

final class GatewayServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $this->mergeConfigFrom(__DIR__.'/../config/gateway.php', 'gateway');

        $this->app->singleton(RegistryRouteCache::class, function (): RegistryRouteCache {
            $enabled = (bool) config('gateway.cache.enabled', true);
            $cache = null;

            if ($enabled && $this->app->bound(CacheFactory::class)) {
                $store = config('gateway.cache.store');
                $cache = $this->app->make(CacheFactory::class)->store(
                    is_string($store) && $store !== '' ? $store : null,
                );
            }

            return new RegistryRouteCache(
                cache: $cache,
                enabled: $enabled,
                pathTtl: max(0, (int) config('gateway.cache.path_ttl', 300)),
                routeTtl: max(0, (int) config('gateway.cache.route_ttl', 30)),
                missingTtl: max(0, (int) config('gateway.cache.missing_ttl', 5)),
                prefix: (string) config('gateway.cache.prefix', 'gateway'),
                cacheCustomProviders: (bool) config('gateway.cache.custom_providers', false),
            );
        });

        $this->app->bind(PathResolver::class, function (): PathResolver {
            $resolver = config('gateway.path_resolver');

            if (! is_string($resolver) || $resolver === '') {
                throw new RuntimeException('Configure gateway.path_resolver for the registry route planner.');
            }

            $resolved = $this->app->make($resolver);
            if (! $resolved instanceof PathResolver) {
                throw new RuntimeException("Gateway path resolver [{$resolver}] must implement PathResolver.");
            }

            return $resolved;
        });

        $this->app->bind(RoutePlanner::class, function (): RoutePlanner {
            $configuredPlanner = config('gateway.planner');
            $configuredResolver = config('gateway.path_resolver');
            $planner = $configuredPlanner
                ?: (is_string($configuredResolver) && $configuredResolver !== '' ? RegistryRoutePlanner::class : null);

            if (! is_string($planner) || $planner === '') {
                throw new RuntimeException('Configure gateway.planner with the Laravel route planner class.');
            }

            $resolved = $this->app->make($planner);
            if (! $resolved instanceof RoutePlanner) {
                throw new RuntimeException("Gateway planner [{$planner}] must implement RoutePlanner.");
            }

            return $resolved;
        });
    }

    public function boot(): void
    {
        $this->publishes([
            __DIR__.'/../config/gateway.php' => config_path('gateway.php'),
        ], 'gateway-config');

        $this->app->booted(function (): void {
            $this->app['router']->post('/_internal/gateway/plan', PlanController::class);
            $this->app['router']
                ->post('/registry/endpoints', EndpointController::class)
                ->name('registry.endpoints.store');
            $this->app['router']
                ->post('/registry/endpoints/{endpointKey}/subscriptions', SubscriptionController::class)
                ->name('registry.subscriptions.store');
            $this->app['router']
                ->get('/registry/endpoints/{endpointKey}/subscriptions', [SubscriptionManagementController::class, 'index'])
                ->name('registry.subscriptions.index');
            $this->app['router']
                ->get('/registry/endpoints/{endpointKey}/subscriptions/{destinationId}', [SubscriptionManagementController::class, 'show'])
                ->name('registry.subscriptions.show');
            $this->app['router']
                ->patch('/registry/endpoints/{endpointKey}/subscriptions/{destinationId}', [SubscriptionManagementController::class, 'update'])
                ->name('registry.subscriptions.update');
            $this->app['router']
                ->patch('/registry/endpoints/{endpointKey}/subscriptions/{destinationId}/status', [SubscriptionManagementController::class, 'status'])
                ->name('registry.subscriptions.status');
            $this->app['router']
                ->delete('/registry/endpoints/{endpointKey}/subscriptions/{destinationId}', [SubscriptionManagementController::class, 'destroy'])
                ->name('registry.subscriptions.destroy');
            $this->app['router']
                ->patch(
                    '/registry/endpoints/{endpointKey}/subscriptions/{destinationId}/match',
                    SubscriptionMatchController::class,
                )
                ->name('registry.subscriptions.match.patch');
        });
    }
}
