<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use Illuminate\Support\ServiceProvider;
use RuntimeException;
use Webong\Gateway\Contracts\CacheablePathResolver;
use Webong\Gateway\Contracts\PathResolver;
use Webong\Gateway\GatewayRouter;
use Webong\Gateway\Planning\PathResolverRegistry;
use Webong\Gateway\Planning\ProtocolPlannerRegistry;
use Webong\Gateway\Protocols\Protocol;
use Webong\Gateway\Resources\ConfigurationKindRegistry;
use Webong\Proxy\WebProxy;

final class DnsServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $this->mergeConfigFrom(__DIR__.'/../config/dns.php', 'gateway.dns');

        $this->app->singleton(DnsProviderRegistry::class, function (): DnsProviderRegistry {
            $registry = new DnsProviderRegistry;
            $registry->register('manual', $this->app->make(ManualDnsProvider::class));
            $registry->register('cloudflare', $this->app->make(CloudflareDnsProvider::class));

            return $registry;
        });
        $this->app->afterResolving(ConfigurationKindRegistry::class,
            static fn (ConfigurationKindRegistry $kinds) => $kinds->register('dns.zone', 'dns.hook'));
        $this->app->afterResolving(ProtocolPlannerRegistry::class, function (ProtocolPlannerRegistry $planners): void {
            $planners->register(Protocol::DNS, fn (): DnsProtocolPlanner => $this->app->make(DnsProtocolPlanner::class));
        });
        $this->app->afterResolving(PathResolverRegistry::class, function (PathResolverRegistry $resolvers): void {
            $resolvers->register('dns', static fn (?PathResolver $fallback): PathResolver =>
                $fallback === null || $fallback instanceof CacheablePathResolver
                    ? new CacheableDnsHookPathResolver($fallback)
                    : new DnsHookPathResolver($fallback));
        });
    }

    public function boot(): void
    {
        $client = trim((string) config('gateway.dns.client', 'gateway'));
        if ($client === '') {
            throw new RuntimeException('Gateway DNS hooks require gateway.dns.client.');
        }
        $proxy = $this->app->make(WebProxy::class);
        if (! $proxy->has($client)) {
            $proxy->register($client, GatewayRouter::class);
        }

        $this->loadRoutesFrom(__DIR__.'/../routes/planner.php');
        $this->publishes([__DIR__.'/../config/dns.php' => config_path('gateway-dns.php')], 'gateway-dns-config');
    }
}
