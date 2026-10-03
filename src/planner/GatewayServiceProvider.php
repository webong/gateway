<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Illuminate\Contracts\Cache\Factory as CacheFactory;
use Illuminate\Support\ServiceProvider;
use RuntimeException;
use Webong\Gateway\Automation\CapabilityAwareRoutePlanner;
use Webong\Gateway\Automation\InMemoryAutomationLanguageRegistry;
use Webong\Gateway\Contracts\AutomationCapability;
use Webong\Gateway\Contracts\AutomationExecutionTarget;
use Webong\Gateway\Contracts\AutomationLanguageRegistry;
use Webong\Gateway\Contracts\CacheablePathResolver;
use Webong\Gateway\Contracts\ExtensionProviderRegistry;
use Webong\Gateway\Contracts\PathResolver;
use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Contracts\ScriptScheduleRegistrar;
use Webong\Gateway\Deliveries\Http\DeliveryController;
use Webong\Gateway\Dns\CacheableDnsHookPathResolver;
use Webong\Gateway\Dns\DnsHookEventRecorder;
use Webong\Gateway\Dns\DnsHookPathResolver;
use Webong\Gateway\Dns\Http\DnsHookController;
use Webong\Gateway\Dns\Http\DnsHookEventController;
use Webong\Gateway\Dns\Http\DnsHookUsageController;
use Webong\Gateway\Dns\Http\DnsZoneController;
use Webong\Gateway\Email\Http\EmailController;
use Webong\Gateway\Email\Http\EmailSubmissionController;
use Webong\Gateway\Email\Http\EmailSuppressionController;
use Webong\Gateway\Email\EmailRegistry;
use Webong\Gateway\Email\EmailMessagePolicy;
use Webong\Gateway\Email\EmailSubmissionAuthenticator;
use Webong\Gateway\Email\EmailSubmissionQuota;
use Webong\Gateway\Email\EmailSuppressionStore;
use Webong\Gateway\Email\PruneEmailSubmissionUsageCommand;
use Webong\Gateway\Email\MailboxAuthenticator;
use Webong\Gateway\Email\NativeTxtResolver;
use Webong\Gateway\Email\DmarcEvaluator;
use Webong\Gateway\Email\SrsCodec;
use Webong\Gateway\Email\SrsReturnStore;
use Webong\Gateway\Email\TxtResolver;
use Webong\Gateway\Events\WebEventRecorder;
use Webong\Gateway\Events\Http\EndpointEventController;
use Webong\Gateway\Events\PruneEventsCommand;
use Webong\Gateway\Reconciliation\Http\Controllers\InternalInstanceController;
use Webong\Gateway\Reconciliation\Http\Controllers\InternalRouteController;
use Webong\Gateway\Reconciliation\Http\Controllers\InternalAssignmentController;
use Webong\Gateway\Reconciliation\Http\Controllers\InternalServerController;
use Webong\Gateway\Reconciliation\Http\Controllers\InternalTargetController;
use Webong\Gateway\Reconciliation\InternalRequestAuthenticator;
use Webong\Gateway\Schedules\Http\InternalScheduleController;
use Webong\Gateway\Schedules\Http\ScheduleController;
use Webong\Gateway\Schedules\Http\ScheduleDeliveryOutcomeController;
use Webong\Gateway\Schedules\PersistScriptSchedules;
use Webong\Gateway\Schedules\SendScheduleNotificationsCommand;
use Webong\Gateway\Servers\Http\Controllers\ApplicationController as ManagedApplicationController;
use Webong\Gateway\Servers\Http\Controllers\InstanceController as ManagedInstanceController;
use Webong\Gateway\Servers\Http\Controllers\ServerController as ManagedServerController;
use Webong\Gateway\Servers\Http\Controllers\ServerLifecycleController;
use Webong\Gateway\Servers\Http\Controllers\TargetController;
use Webong\Gateway\Servers\ServerRegistry;
use Webong\Gateway\Servers\ServerTypeRegistry;
use Webong\WebProxy\WebProxy;

final class GatewayServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $this->mergeConfigFrom(__DIR__.'/config/gateway.php', 'gateway');

        $this->app->bind(TxtResolver::class, NativeTxtResolver::class);
        $this->app->bind(SrsReturnStore::class, function (): SrsReturnStore {
            $encoded = config('gateway.email.srs_keys');
            if (! is_array($encoded) || ! array_is_list($encoded) || $encoded === []) {
                throw new RuntimeException('Gateway SRS requires a JSON array of persistent base64 keys.');
            }
            $keys = [];
            foreach ($encoded as $item) {
                $key = is_string($item) ? base64_decode($item, true) : false;
                if ($key === false || strlen($key) < 32) {
                    throw new RuntimeException('Gateway SRS keys must decode to at least 32 bytes.');
                }
                $keys[] = $key;
            }

            return new SrsReturnStore(
                new SrsCodec(
                    strtolower(trim((string) config('gateway.email.srs_domain', ''))),
                    $keys,
                    (int) config('gateway.email.srs_max_age_seconds', 21 * 86400),
                ),
                $this->app->make(EmailRegistry::class),
            );
        });

        foreach (ExtensionProviderRegistry::providers() as $provider) {
            $this->app->register($provider);
        }

        $this->app->singleton(ServerTypeRegistry::class);
        $this->app->singleton(ServerRegistry::class);
        $this->app->bind(ScriptScheduleRegistrar::class, PersistScriptSchedules::class);
        $this->app->singleton(InternalRequestAuthenticator::class);
        $this->app->singleton(AutomationLanguageRegistry::class, function (): AutomationLanguageRegistry {
            $registry = new InMemoryAutomationLanguageRegistry;
            $languages = config('gateway.automation.languages', []);

            if (! is_array($languages)) {
                throw new RuntimeException('gateway.automation.languages must be a list of language identifiers.');
            }

            foreach ($languages as $language) {
                if (! is_string($language)) {
                    throw new RuntimeException('gateway.automation.languages must contain only strings.');
                }

                $registry->register(
                    $language,
                    AutomationExecutionTarget::SPINNER,
                    AutomationCapability::ROUTE,
                    AutomationCapability::SCHEDULE,
                );
            }

            return $registry;
        });

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
            $fallback = null;
            if (is_string($resolver) && $resolver !== '' && $resolver !== DnsHookPathResolver::class) {
                $fallback = $this->app->make($resolver);
                if (! $fallback instanceof PathResolver) {
                    throw new RuntimeException("Gateway path resolver [{$resolver}] must implement PathResolver.");
                }
            }

            if ((bool) config('gateway.dns.enabled', false)) {
                return $fallback === null || $fallback instanceof CacheablePathResolver
                    ? new CacheableDnsHookPathResolver($fallback)
                    : new DnsHookPathResolver($fallback);
            }

            return $fallback ?? throw new RuntimeException('Configure gateway.path_resolver for the registry route planner.');
        });

        $this->app->bind(RoutePlanner::class, function (): RoutePlanner {
            $configuredPlanner = config('gateway.planner');
            $configuredResolver = config('gateway.path_resolver');
            $hasResolver = (is_string($configuredResolver) && $configuredResolver !== '')
                || (bool) config('gateway.dns.enabled', false);
            $planner = $configuredPlanner
                ?: ($hasResolver ? RegistryRoutePlanner::class : null);

            if (! is_string($planner) || $planner === '') {
                throw new RuntimeException('Configure gateway.planner with the Laravel route planner class.');
            }

            $resolved = $this->app->make($planner);
            if (! $resolved instanceof RoutePlanner) {
                throw new RuntimeException("Gateway planner [{$planner}] must implement RoutePlanner.");
            }

            return $resolved;
        });

        $this->app->bind(CapabilityAwareRoutePlanner::class, fn (): CapabilityAwareRoutePlanner => new CapabilityAwareRoutePlanner(
            planner: $this->app->make(RoutePlanner::class),
            languages: $this->app->make(AutomationLanguageRegistry::class),
            events: $this->app->make(WebEventRecorder::class),
        ));

        $this->app->bind(RegistryProtocolPlanner::class, fn (): RegistryProtocolPlanner => new RegistryProtocolPlanner(
            routePlanner: $this->app->make(CapabilityAwareRoutePlanner::class),
            dnsEvents: $this->app->make(DnsHookEventRecorder::class),
            email: $this->app->make(EmailRegistry::class),
            dmarc: $this->app->make(DmarcEvaluator::class),
            srs: config('gateway.email.srs_enabled', false) ? $this->app->make(SrsReturnStore::class) : null,
            emailPolicy: $this->emailMessagePolicy(),
            submissionAuthenticator: $this->emailSubmissionAuthenticator(),
            submissionQuota: $this->app->make(EmailSubmissionQuota::class),
            suppressions: $this->app->make(EmailSuppressionStore::class),
            mailboxAuthenticator: $this->mailboxAuthenticator(),
        ));

        $this->app->bind(ProtocolPlanner::class, function (): ProtocolPlanner {
            $configuredPlanner = config('gateway.protocol_planner');
            $configuredResolver = config('gateway.path_resolver');
            $hasResolver = (is_string($configuredResolver) && $configuredResolver !== '')
                || (bool) config('gateway.dns.enabled', false);
            $planner = $configuredPlanner
                ?: ($hasResolver ? RegistryProtocolPlanner::class : null);

            if (! is_string($planner) || $planner === '') {
                throw new RuntimeException('Configure gateway.protocol_planner or gateway.path_resolver for protocol events.');
            }

            $resolved = $this->app->make($planner);
            if (! $resolved instanceof ProtocolPlanner) {
                throw new RuntimeException("Gateway protocol planner [{$planner}] must implement ProtocolPlanner.");
            }

            return $resolved;
        });
    }

    private function emailMessagePolicy(): ?EmailMessagePolicy
    {
        $configured = config('gateway.email.policy');
        if ($configured === null || $configured === '') {
            return null;
        }
        if (! is_string($configured)) {
            throw new RuntimeException('Gateway Email policy must be a configured class name.');
        }
        $policy = $this->app->make($configured);
        if (! $policy instanceof EmailMessagePolicy) {
            throw new RuntimeException("Gateway Email policy [{$configured}] must implement EmailMessagePolicy.");
        }

        return $policy;
    }

    private function emailSubmissionAuthenticator(): ?EmailSubmissionAuthenticator
    {
        $configured = config('gateway.email.submission_authenticator');
        if ($configured === null || $configured === '') {
            return null;
        }
        if (! is_string($configured)) {
            throw new RuntimeException('Gateway Email submission authenticator must be a configured class name.');
        }
        $authenticator = $this->app->make($configured);
        if (! $authenticator instanceof EmailSubmissionAuthenticator) {
            throw new RuntimeException("Gateway Email submission authenticator [{$configured}] must implement EmailSubmissionAuthenticator.");
        }

        return $authenticator;
    }

    private function mailboxAuthenticator(): ?MailboxAuthenticator
    {
        $configured = config('gateway.email.mailbox_authenticator');
        if ($configured === null || $configured === '') {
            return null;
        }
        if (! is_string($configured)) {
            throw new RuntimeException('Gateway mailbox authenticator must be a configured class name.');
        }
        $authenticator = $this->app->make($configured);
        if (! $authenticator instanceof MailboxAuthenticator) {
            throw new RuntimeException("Gateway mailbox authenticator [{$configured}] must implement MailboxAuthenticator.");
        }

        return $authenticator;
    }

    public function boot(): void
    {
        $this->loadMigrationsFrom(__DIR__.'/database/migrations');
        if ($this->app->runningInConsole()) {
            $this->commands([PruneEventsCommand::class, PruneEmailSubmissionUsageCommand::class, SendScheduleNotificationsCommand::class]);
        }

        if ((bool) config('gateway.dns.enabled', false)) {
            $client = trim((string) config('gateway.dns.client', 'gateway'));
            if ($client === '') {
                throw new RuntimeException('Gateway DNS hooks require gateway.dns.client.');
            }
            $webProxy = $this->app->make(WebProxy::class);
            if (! $webProxy->has($client)) {
                $webProxy->register($client, GatewayRouter::class);
            }
        }

        $this->publishes([
            __DIR__.'/config/gateway.php' => config_path('gateway.php'),
        ], 'gateway-config');

        $this->app->booted(function (): void {
            $this->app['router']->get('/_internal/gateway/ready', InternalReadinessController::class);
            $this->app['router']->post('/_internal/gateway/plan', PlanController::class);
            $this->app['router']->post('/_internal/gateway/event', ProtocolPlanController::class);
            $this->app['router']->post('/_internal/gateway/schedules/claim', [InternalScheduleController::class, 'claim']);
            $this->app['router']->post('/_internal/gateway/schedule-runs/{run}/complete', [InternalScheduleController::class, 'complete']);
            $this->app['router']->post('/_internal/gateway/schedules/register', [InternalScheduleController::class, 'register']);
            $this->app['router']->post('/_internal/gateway/schedules/notify', [InternalScheduleController::class, 'notify']);
            $this->app['router']->get('/_internal/provisioning/servers', InternalServerController::class);
            $this->app['router']->get('/_internal/provisioning/routes/resolve', InternalRouteController::class);
            $this->app['router']->put('/_internal/provisioning/nodes/{node}/assignments', InternalAssignmentController::class);
            $this->app['router']->delete('/_internal/provisioning/nodes/{node}/session', [InternalAssignmentController::class, 'release']);
            $this->app['router']->post('/_internal/provisioning/instances/reset', [InternalInstanceController::class, 'resetNode']);
            $this->app['router']->put('/_internal/provisioning/servers/{server}/instances/{instance}', [InternalInstanceController::class, 'update']);

            $this->app['router']->get('/servers', [ManagedServerController::class, 'index']);
            $this->app['router']->get('/targets', [TargetController::class, 'index']);
            $this->app['router']->post('/targets', [TargetController::class, 'store']);
            $this->app['router']->get('/targets/{target}', [TargetController::class, 'show']);
            $this->app['router']->delete('/targets/{target}', [TargetController::class, 'destroy']);
            $this->app['router']->post('/targets/{target}/rotate-token', [TargetController::class, 'rotateToken']);
            $this->app['router']->get('/_internal/provisioning/targets/{target}', InternalTargetController::class);
            $this->app['router']->post('/servers', [ManagedServerController::class, 'store']);
            $this->app['router']->get('/servers/{server}', [ManagedServerController::class, 'show']);
            $this->app['router']->patch('/servers/{server}', [ManagedServerController::class, 'update']);
            $this->app['router']->delete('/servers/{server}', [ManagedServerController::class, 'destroy']);
            $this->app['router']->post('/servers/{server}/start', [ServerLifecycleController::class, 'start']);
            $this->app['router']->post('/servers/{server}/stop', [ServerLifecycleController::class, 'stop']);
            $this->app['router']->post('/servers/{server}/restart', [ServerLifecycleController::class, 'restart']);
            $this->app['router']->post('/servers/{server}/scale', [ServerLifecycleController::class, 'scale']);
            $this->app['router']->get('/servers/{server}/health', [ServerLifecycleController::class, 'health']);

            $this->app['router']->get('/applications', [ManagedApplicationController::class, 'index']);
            $this->app['router']->post('/applications', [ManagedApplicationController::class, 'store']);
            $this->app['router']->get('/applications/{application}', [ManagedApplicationController::class, 'show']);
            $this->app['router']->patch('/applications/{application}', [ManagedApplicationController::class, 'update']);
            $this->app['router']->delete('/applications/{application}', [ManagedApplicationController::class, 'destroy']);

            $this->app['router']->get('/instances', [ManagedInstanceController::class, 'index']);
            $this->app['router']->get('/instances/{instance}', [ManagedInstanceController::class, 'show']);
            $this->app['router']->get('/deliveries', [DeliveryController::class, 'index']);
            $this->app['router']->get('/deliveries/{delivery}', [DeliveryController::class, 'show']);
            $this->app['router']->post('/deliveries/{delivery}/retry', [DeliveryController::class, 'retry']);
            $this->app['router']->get('/email/domains', [EmailController::class, 'domains']);
            $this->app['router']->post('/email/submissions', EmailSubmissionController::class);
            $this->app['router']->get('/email/suppressions', [EmailSuppressionController::class, 'index']);
            $this->app['router']->post('/email/suppressions', [EmailSuppressionController::class, 'store']);
            $this->app['router']->delete('/email/suppressions/{suppression}', [EmailSuppressionController::class, 'destroy']);
            $this->app['router']->post('/email/domains', [EmailController::class, 'createDomain']);
            $this->app['router']->get('/email/domains/{domain}', [EmailController::class, 'showDomain']);
            $this->app['router']->post('/email/domains/{domain}/verify', [EmailController::class, 'verifyDomain']);
            $this->app['router']->patch('/email/domains/{domain}', [EmailController::class, 'updateDomain']);
            $this->app['router']->delete('/email/domains/{domain}', [EmailController::class, 'deleteDomain']);
            $this->app['router']->get('/email/domains/{domain}/aliases', [EmailController::class, 'aliases']);
            $this->app['router']->post('/email/domains/{domain}/aliases', [EmailController::class, 'createAlias']);
            $this->app['router']->get('/email/domains/{domain}/aliases/{alias}', [EmailController::class, 'showAlias']);
            $this->app['router']->patch('/email/domains/{domain}/aliases/{alias}', [EmailController::class, 'updateAlias']);
            $this->app['router']->delete('/email/domains/{domain}/aliases/{alias}', [EmailController::class, 'deleteAlias']);
            $this->app['router']->get('/schedules', [ScheduleController::class, 'index']);
            $this->app['router']->post('/schedules', [ScheduleController::class, 'store']);
            $this->app['router']->get('/schedules/{schedule}', [ScheduleController::class, 'show']);
            $this->app['router']->patch('/schedules/{schedule}', [ScheduleController::class, 'update']);
            $this->app['router']->delete('/schedules/{schedule}', [ScheduleController::class, 'destroy']);
            $this->app['router']->get('/schedules/{schedule}/runs', [ScheduleController::class, 'runs']);
            $this->app['router']->get('/schedules/{schedule}/runs/{run}/outcome', ScheduleDeliveryOutcomeController::class);
            $this->app['router']->post('/schedules/{schedule}/runs', [ScheduleController::class, 'run']);
            if ((bool) config('gateway.dns.enabled', false)) {
                $this->app['router']->get('/dns/zones', DnsZoneController::class)->name('dns.zones.index');
                $this->app['router']->get('/dns/hooks', [DnsHookController::class, 'index'])->name('dns.hooks.index');
                $this->app['router']->post('/dns/hooks', [DnsHookController::class, 'store'])->name('dns.hooks.store');
                $this->app['router']->get('/dns/hooks/{hook}', [DnsHookController::class, 'show'])->name('dns.hooks.show');
                $this->app['router']->patch('/dns/hooks/{hook}', [DnsHookController::class, 'update'])->name('dns.hooks.update');
                $this->app['router']->delete('/dns/hooks/{hook}', [DnsHookController::class, 'destroy'])->name('dns.hooks.destroy');
                $this->app['router']->get('/dns/hooks/{hook}/events', DnsHookEventController::class)->name('dns.hooks.events');
                $this->app['router']->get('/dns/hooks/{hook}/usage', DnsHookUsageController::class)->name('dns.hooks.usage');
            }
            $this->app['router']
                ->post('/registry/endpoints', EndpointController::class)
                ->name('registry.endpoints.store');
            $this->app['router']
                ->get('/registry/endpoints/{endpointKey}/events', EndpointEventController::class)
                ->name('registry.endpoints.events');
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
