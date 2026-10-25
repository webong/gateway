<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use Illuminate\Support\ServiceProvider;
use Webong\Gateway\Contracts\CacheablePathResolver;
use Webong\Gateway\Contracts\PathResolver;
use Webong\Gateway\Companion\ExtensionRuntimeConfigurationRegistry;
use Webong\Gateway\GatewayRouter;
use Webong\Gateway\Observations\GatewayObservationRegistry;
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

        $this->app->singleton(DnsProvisionerRegistry::class, function (): DnsProvisionerRegistry {
            $registry = new DnsProvisionerRegistry;
            $registry->register('manual', $this->app->make(ManualDnsProvisioner::class));
            $registry->register('cloudflare', $this->app->make(CloudflareDnsProvisioner::class));

            return $registry;
        });
        $this->app->afterResolving(ConfigurationKindRegistry::class,
            static fn (ConfigurationKindRegistry $kinds) => $kinds->register('dns.zone', 'dns.forwarder'));
        $this->app->afterResolving(ExtensionRuntimeConfigurationRegistry::class,
            function (ExtensionRuntimeConfigurationRegistry $runtime): void {
                $runtime->register('webong/dns', function (array $configuration): array {
                    $configuration['zones'] = $this->app->make(DnsZoneCatalog::class)->all();
                    $configuration['forwarders'] = $this->app->make(DnsForwarderStore::class)->active();

                    return $configuration;
                });
            });
        $this->app->afterResolving(ProtocolPlannerRegistry::class, function (ProtocolPlannerRegistry $planners): void {
            $planners->register(Protocol::DNS, fn (): DnsProtocolPlanner => $this->app->make(DnsProtocolPlanner::class));
        });
        $this->app->afterResolving(GatewayObservationRegistry::class, function (GatewayObservationRegistry $observations): void {
            $observations->register('dns.query', fn ($observation) => $this->app->make(DnsQueryObservationRecorder::class)->record($observation));
        });
        $this->app->afterResolving(PathResolverRegistry::class, function (PathResolverRegistry $resolvers): void {
            $resolvers->register('dns', static fn (?PathResolver $fallback): PathResolver =>
                $fallback === null || $fallback instanceof CacheablePathResolver
                    ? new CacheableDnsEndpointPathResolver($fallback)
                    : new DnsEndpointPathResolver($fallback));
        });
    }

    public function boot(): void
    {
        $proxy = $this->app->make(WebProxy::class);
        if (! $proxy->has(DnsEndpointRegistry::CLIENT)) {
            $proxy->register(DnsEndpointRegistry::CLIENT, GatewayRouter::class);
        }

        $this->loadRoutesFrom(__DIR__.'/../routes/planner.php');
        $this->publishes([__DIR__.'/../config/dns.php' => config_path('gateway-dns.php')], 'gateway-dns-config');
    }
}
