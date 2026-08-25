<?php

declare(strict_types=1);

namespace Webong\WebRelay;

use Illuminate\Contracts\Cache\Factory as CacheFactory;
use Illuminate\Support\ServiceProvider;
use RuntimeException;
use Webong\WebRelay\Contracts\PathResolver;
use Webong\WebRelay\Contracts\RoutePlanner;

final class WebRelayServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $this->mergeConfigFrom(__DIR__.'/../config/web-relay.php', 'web-relay');

        $this->app->singleton(RegistryRouteCache::class, function (): RegistryRouteCache {
            $enabled = (bool) config('web-relay.cache.enabled', true);
            $cache = null;

            if ($enabled && $this->app->bound(CacheFactory::class)) {
                $store = config('web-relay.cache.store');
                $cache = $this->app->make(CacheFactory::class)->store(
                    is_string($store) && $store !== '' ? $store : null,
                );
            }

            return new RegistryRouteCache(
                cache: $cache,
                enabled: $enabled,
                pathTtl: max(0, (int) config('web-relay.cache.path_ttl', 300)),
                routeTtl: max(0, (int) config('web-relay.cache.route_ttl', 30)),
                missingTtl: max(0, (int) config('web-relay.cache.missing_ttl', 5)),
                prefix: (string) config('web-relay.cache.prefix', 'web-relay'),
                cacheCustomProviders: (bool) config('web-relay.cache.custom_providers', false),
            );
        });

        $this->app->bind(PathResolver::class, function (): PathResolver {
            $resolver = config('web-relay.path_resolver');

            if (! is_string($resolver) || $resolver === '') {
                throw new RuntimeException('Configure web-relay.path_resolver for the registry route planner.');
            }

            $resolved = $this->app->make($resolver);
            if (! $resolved instanceof PathResolver) {
                throw new RuntimeException("Web-relay path resolver [{$resolver}] must implement PathResolver.");
            }

            return $resolved;
        });

        $this->app->bind(RoutePlanner::class, function (): RoutePlanner {
            $configuredPlanner = config('web-relay.planner');
            $configuredResolver = config('web-relay.path_resolver');
            $planner = $configuredPlanner
                ?: (is_string($configuredResolver) && $configuredResolver !== '' ? RegistryRoutePlanner::class : null);

            if (! is_string($planner) || $planner === '') {
                throw new RuntimeException('Configure web-relay.planner with the Laravel route planner class.');
            }

            $resolved = $this->app->make($planner);
            if (! $resolved instanceof RoutePlanner) {
                throw new RuntimeException("Web-relay planner [{$planner}] must implement RoutePlanner.");
            }

            return $resolved;
        });
    }

    public function boot(): void
    {
        $this->publishes([
            __DIR__.'/../config/web-relay.php' => config_path('web-relay.php'),
        ], 'web-relay-config');

        $this->app->booted(function (): void {
            $this->app['router']->post('/_internal/web-relay/plan', PlanController::class);
            $this->app['router']
                ->post('/registry/endpoints', EndpointController::class)
                ->name('registry.endpoints.store');
            $this->app['router']
                ->post('/registry/endpoints/{endpointKey}/subscriptions', SubscriptionController::class)
                ->name('registry.subscriptions.store');
        });
    }
}
