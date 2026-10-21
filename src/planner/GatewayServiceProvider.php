<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Illuminate\Contracts\Cache\Factory as CacheFactory;
use Illuminate\Support\ServiceProvider;
use RuntimeException;
use Webong\Gateway\Accounts\AccountStore;
use Webong\Gateway\Accounts\Http\AccountController;
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
use Webong\Gateway\Dns\CloudflareDnsProvider;
use Webong\Gateway\Dns\DnsHookEventRecorder;
use Webong\Gateway\Dns\DnsHookPathResolver;
use Webong\Gateway\Dns\DnsProviderRegistry;
use Webong\Gateway\Dns\Http\DnsHookController;
use Webong\Gateway\Dns\Http\DnsHookEventController;
use Webong\Gateway\Dns\Http\DnsHookUsageController;
use Webong\Gateway\Dns\Http\DnsZoneController;
use Webong\Gateway\Dns\Http\InternalDnsZoneController;
use Webong\Gateway\Dns\ManualDnsProvider;
use Webong\Gateway\Email\Http\EmailController;
use Webong\Gateway\Email\Http\EmailSubmissionController;
use Webong\Gateway\Email\Http\EmailSubmissionPolicyController;
use Webong\Gateway\Email\Http\EmailRuntimePolicyController;
use Webong\Gateway\Email\Http\EmailVerificationProfileController;
use Webong\Gateway\Email\Http\EmailSuppressionController;
use Webong\Gateway\Email\Http\MailHookController;
use Webong\Gateway\Email\Http\MailboxConnectionController;
use Webong\Gateway\Email\Http\ImapConnectionController;
use Webong\Gateway\Email\Http\MailboxPollController;
use Webong\Gateway\Email\Http\InternalMailboxPollController;
use Webong\Gateway\Email\Http\WorkerIngressController;
use Webong\Gateway\Email\Http\InternalWorkerIngressController;
use Webong\Gateway\Email\Http\SendingConnectionController;
use Webong\Gateway\Email\Http\ManagedSendingController;
use Webong\Gateway\Email\Http\AccountReceivingController;
use Webong\Gateway\Email\Http\InternalMailboxTransportController;
use Webong\Gateway\Email\MailHookPathResolver;
use Webong\Gateway\Email\CacheableMailHookPathResolver;
use Webong\Gateway\Email\MailHookRegistry;
use Webong\Gateway\Email\AccountReceivingStore;
use Webong\Gateway\Email\ManagedSendingStore;
use Webong\Gateway\Email\MailboxConnectionStore;
use Webong\Gateway\Email\MailboxOAuthRefresher;
use Webong\Gateway\Email\EmailRegistry;
use Webong\Gateway\Email\EmailMessagePolicy;
use Webong\Gateway\Email\EmailSubmissionAuthenticator;
use Webong\Gateway\Email\EmailSubmissionQuota;
use Webong\Gateway\Email\EmailSubmissionPolicyStore;
use Webong\Gateway\Email\EmailRuntimePolicyStore;
use Webong\Gateway\Email\EmailSuppressionStore;
use Webong\Gateway\Email\PruneEmailSubmissionUsageCommand;
use Webong\Gateway\Email\MailboxAuthenticator;
use Webong\Gateway\Email\NativeTxtResolver;
use Webong\Gateway\Email\MxResolver;
use Webong\Gateway\Email\NativeMxResolver;
use Webong\Gateway\Email\DmarcEvaluator;
use Webong\Gateway\Email\EmailSrsPolicyStore;
use Webong\Gateway\Email\EmailUpstreamCatalog;
use Webong\Gateway\Email\EmailHostedMailCatalog;
use Webong\Gateway\Email\PlannerHostedMailRouteResolver;
use Webong\Gateway\Email\Http\EmailSrsPolicyController;
use Webong\Gateway\Email\Http\EmailUpstreamController;
use Webong\Gateway\Email\Http\EmailHostedMailController;
use Webong\Gateway\Email\Http\InternalHostedMailAdapterController;
use Webong\Gateway\Email\Http\EmailDkimKeyController;
use Webong\Gateway\Email\Http\InternalDkimKeyController;
use Webong\Gateway\Email\SrsReturnStore;
use Webong\Gateway\Email\TxtResolver;
use Webong\Gateway\Events\WebEventRecorder;
use Webong\Gateway\Events\Http\WebEventPolicyController;
use Webong\Gateway\Events\Http\EndpointEventController;
use Webong\Gateway\Events\PruneEventsCommand;
use Webong\Gateway\Reconciliation\Http\Controllers\InternalInstanceController;
use Webong\Gateway\Reconciliation\Http\Controllers\InternalRouteController;
use Webong\Gateway\Reconciliation\Http\Controllers\InternalAssignmentController;
use Webong\Gateway\Reconciliation\Http\Controllers\InternalServerController;
use Webong\Gateway\Reconciliation\Http\Controllers\InternalTargetController;
use Webong\Gateway\Reconciliation\InternalRequestAuthenticator;
use Webong\Gateway\Resources\Http\ConfigurationController;
use Webong\Gateway\Resources\PlannerResourceStore;
use Webong\Gateway\Resources\Http\ShellHookGrantController;
use Webong\Gateway\Schedules\Http\InternalScheduleController;
use Webong\Gateway\Schedules\Http\ScheduleController;
use Webong\Gateway\Schedules\Http\ScheduleNotificationConfigController;
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
use Webong\Gateway\Webstream\Http\WebstreamPrivateSourceController;
use Webong\Gateway\Webstream\Http\WebstreamRoutePolicyController;
use Webong\Gateway\Webstream\WebstreamPrivateSourceStore;
use Webong\Gateway\Webstream\WebstreamRoutePolicyStore;
use Webong\WebProxy\WebProxy;

final class GatewayServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $this->mergeConfigFrom(__DIR__.'/config/gateway.php', 'gateway');

        $this->app->bind(TxtResolver::class, NativeTxtResolver::class);
        $this->app->bind(MxResolver::class, NativeMxResolver::class);
        $this->app->bind(MailboxConnectionStore::class, function (): MailboxConnectionStore {
            $configured = config('gateway.email.oauth_refresher');
            $refresher = null;
            if (is_string($configured) && $configured !== '') {
                $refresher = $this->app->make($configured);
                if (! $refresher instanceof MailboxOAuthRefresher) {
                    throw new RuntimeException('Gateway mailbox OAuth refresher must implement MailboxOAuthRefresher.');
                }
            }

            return new MailboxConnectionStore($refresher, $this->app->make(EmailUpstreamCatalog::class),
                $this->app->make(PlannerResourceStore::class), $this->app->make(AccountStore::class));
        });
        $this->app->bind(SrsReturnStore::class, fn (): SrsReturnStore => new SrsReturnStore(
            $this->app->make(EmailSrsPolicyStore::class), $this->app->make(EmailRegistry::class),
        ));

        foreach (ExtensionProviderRegistry::providers() as $provider) {
            $this->app->register($provider);
        }

        $this->app->singleton(ServerTypeRegistry::class);
        $this->app->singleton(ServerRegistry::class);
        $this->app->singleton(DnsProviderRegistry::class, function (): DnsProviderRegistry {
            $registry = new DnsProviderRegistry;
            $registry->register('manual', $this->app->make(ManualDnsProvider::class));
            $registry->register('cloudflare', $this->app->make(CloudflareDnsProvider::class));

            return $registry;
        });
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

            $fallback = $fallback === null || $fallback instanceof CacheablePathResolver
                ? new CacheableDnsHookPathResolver($fallback)
                : new DnsHookPathResolver($fallback);

            $hooks = $this->app->make(MailHookRegistry::class);
            return $fallback === null || $fallback instanceof CacheablePathResolver
                ? new CacheableMailHookPathResolver($hooks, $fallback)
                : new MailHookPathResolver($hooks, $fallback);
        });

        $this->app->bind(RoutePlanner::class, function (): RoutePlanner {
            $configuredPlanner = config('gateway.planner');
            $planner = $configuredPlanner ?: RegistryRoutePlanner::class;

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
            srs: $this->app->make(SrsReturnStore::class),
            emailPolicy: $this->emailMessagePolicy(),
            submissionAuthenticator: $this->emailSubmissionAuthenticator(),
            submissionQuota: $this->app->make(EmailSubmissionQuota::class),
            emailSubmissionPolicies: $this->app->make(EmailSubmissionPolicyStore::class),
            suppressions: $this->app->make(EmailSuppressionStore::class),
            mailboxAuthenticator: $this->mailboxAuthenticator(),
            mailHooks: $this->app->make(MailHookRegistry::class),
            receiving: $this->app->make(AccountReceivingStore::class),
            emailRuntimePolicy: $this->app->make(EmailRuntimePolicyStore::class),
            providerRoutes: $this->emailProviderRouteResolver(),
            webstreamPrivateSources: $this->app->make(WebstreamPrivateSourceStore::class),
            webstreamRoutePolicies: $this->app->make(WebstreamRoutePolicyStore::class),
        ));

        $this->app->bind(ProtocolPlanner::class, function (): ProtocolPlanner {
            $configuredPlanner = config('gateway.protocol_planner');
            $planner = $configuredPlanner ?: RegistryProtocolPlanner::class;

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

    private function emailProviderRouteResolver(): ?\Webong\Gateway\Email\EmailProviderRouteResolver
    {
        $configured = config('gateway.email.provider_route_resolver');
        $resolver = null;
        if ($configured !== null && $configured !== '') {
            if (! is_string($configured)) { throw new RuntimeException('Gateway Email provider route resolver must be a class name.'); }
            $resolver = $this->app->make($configured);
            if (! $resolver instanceof \Webong\Gateway\Email\EmailProviderRouteResolver) {
                throw new RuntimeException('Gateway Email provider route resolver must implement EmailProviderRouteResolver.');
            }
        }

        return new PlannerHostedMailRouteResolver($this->app->make(EmailHostedMailCatalog::class), $resolver);
    }

    public function boot(): void
    {
        $this->loadMigrationsFrom(__DIR__.'/database/migrations');
        if ($this->app->runningInConsole()) {
            $this->commands([PruneEventsCommand::class, PruneEmailSubmissionUsageCommand::class, SendScheduleNotificationsCommand::class]);
        }

        $client = trim((string) config('gateway.dns.client', 'gateway'));
        if ($client === '') {
            throw new RuntimeException('Gateway DNS hooks require gateway.dns.client.');
        }
        $webProxy = $this->app->make(WebProxy::class);
        if (! $webProxy->has($client)) {
            $webProxy->register($client, GatewayRouter::class);
        }

        $client = trim((string) config('gateway.email.hook_client', 'gateway'));
        if ($client === '') { throw new RuntimeException('Gateway mail hooks require gateway.email.hook_client.'); }
        $webProxy = $this->app->make(WebProxy::class);
        if (! $webProxy->has($client)) { $webProxy->register($client, GatewayRouter::class); }

        $this->publishes([
            __DIR__.'/config/gateway.php' => config_path('gateway.php'),
        ], 'gateway-config');

        $this->app->booted(function (): void {
            $this->app['router']->get('/_internal/gateway/ready', InternalReadinessController::class);
            $this->app['router']->get('/_internal/gateway/shell/executions/{execution}', ShellSshExecutionController::class);
            $this->app['router']->post('/_internal/gateway/plan', PlanController::class);
            $this->app['router']->post('/_internal/gateway/event', ProtocolPlanController::class);
            $this->app['router']->get('/_internal/gateway/dns/zones', InternalDnsZoneController::class);
            $this->app['router']->get('/_internal/gateway/mailbox-connections/{connection}/smtp', InternalMailboxTransportController::class);
            $this->app['router']->get('/_internal/gateway/email/hosted-adapters/{name}', InternalHostedMailAdapterController::class);
            $this->app['router']->get('/_internal/gateway/email/dkim/{domain}', InternalDkimKeyController::class);
            $this->app['router']->post('/_internal/gateway/schedules/claim', [InternalScheduleController::class, 'claim']);
            $this->app['router']->post('/_internal/gateway/schedule-runs/{run}/complete', [InternalScheduleController::class, 'complete']);
            $this->app['router']->post('/_internal/gateway/schedules/register', [InternalScheduleController::class, 'register']);
            $this->app['router']->post('/_internal/gateway/schedules/notify', [InternalScheduleController::class, 'notify']);
            $this->app['router']->post('/_internal/gateway/mailbox-polls/{poll}/claim', [InternalMailboxPollController::class, 'claim']);
            $this->app['router']->post('/_internal/gateway/mailbox-polls/{poll}/ack', [InternalMailboxPollController::class, 'ack']);
            $this->app['router']->post('/_internal/gateway/mailbox-polls/{poll}/release', [InternalMailboxPollController::class, 'release']);
            $this->app['router']->post('/_internal/gateway/worker-ingress/{route}/authorize', [InternalWorkerIngressController::class, 'authorize']);
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
            $this->app['router']->get('/configurations/kinds', [ConfigurationController::class, 'kinds']);
            $this->app['router']->get('/configurations/{kind}/{id}/revisions', [ConfigurationController::class, 'revisions']);
            $this->app['router']->get('/web/event-policy', [WebEventPolicyController::class, 'show']);
            $this->app['router']->post('/web/event-policy', [WebEventPolicyController::class, 'store']);
            $this->app['router']->put('/web/event-policy', [WebEventPolicyController::class, 'replace']);
            $this->app['router']->delete('/web/event-policy', [WebEventPolicyController::class, 'destroy']);
            $this->app['router']->get('/email/runtime-policy', [EmailRuntimePolicyController::class, 'show']);
            $this->app['router']->post('/email/runtime-policy', [EmailRuntimePolicyController::class, 'store']);
            $this->app['router']->put('/email/runtime-policy', [EmailRuntimePolicyController::class, 'replace']);
            $this->app['router']->delete('/email/runtime-policy', [EmailRuntimePolicyController::class, 'destroy']);
            $this->app['router']->get('/email/verification-profile', [EmailVerificationProfileController::class, 'show']);
            $this->app['router']->post('/email/verification-profile', [EmailVerificationProfileController::class, 'store']);
            $this->app['router']->put('/email/verification-profile', [EmailVerificationProfileController::class, 'replace']);
            $this->app['router']->get('/shell/hook-grants', [ShellHookGrantController::class, 'index']);
            $this->app['router']->post('/shell/hook-grants', [ShellHookGrantController::class, 'store']);
            $this->app['router']->get('/shell/hook-grants/{grant}', [ShellHookGrantController::class, 'show']);
            $this->app['router']->put('/shell/hook-grants/{grant}', [ShellHookGrantController::class, 'replace']);
            $this->app['router']->delete('/shell/hook-grants/{grant}', [ShellHookGrantController::class, 'destroy']);
            $this->app['router']->get('/email/submission-policy', [EmailSubmissionPolicyController::class, 'show']);
            $this->app['router']->get('/email/srs-policy', [EmailSrsPolicyController::class, 'show']);
            $this->app['router']->post('/email/srs-policy', [EmailSrsPolicyController::class, 'store']);
            $this->app['router']->put('/email/srs-policy', [EmailSrsPolicyController::class, 'replace']);
            $this->app['router']->delete('/email/srs-policy', [EmailSrsPolicyController::class, 'destroy']);
            $this->app['router']->get('/email/upstreams', [EmailUpstreamController::class, 'index']);
            $this->app['router']->post('/email/upstreams', [EmailUpstreamController::class, 'store']);
            $this->app['router']->get('/email/upstreams/{upstream}', [EmailUpstreamController::class, 'show']);
            $this->app['router']->put('/email/upstreams/{upstream}', [EmailUpstreamController::class, 'replace']);
            $this->app['router']->delete('/email/upstreams/{upstream}', [EmailUpstreamController::class, 'destroy']);
            $this->app['router']->get('/email/hosted-adapters', [EmailHostedMailController::class, 'adapters']);
            $this->app['router']->post('/email/hosted-adapters', [EmailHostedMailController::class, 'createAdapter']);
            $this->app['router']->get('/email/hosted-adapters/{id}', [EmailHostedMailController::class, 'showAdapter']);
            $this->app['router']->put('/email/hosted-adapters/{id}', [EmailHostedMailController::class, 'replaceAdapter']);
            $this->app['router']->delete('/email/hosted-adapters/{id}', [EmailHostedMailController::class, 'deleteAdapter']);
            $this->app['router']->get('/email/hosted-routes', [EmailHostedMailController::class, 'routes']);
            $this->app['router']->post('/email/hosted-routes', [EmailHostedMailController::class, 'createRoute']);
            $this->app['router']->get('/email/hosted-routes/{id}', [EmailHostedMailController::class, 'showRoute']);
            $this->app['router']->put('/email/hosted-routes/{id}', [EmailHostedMailController::class, 'replaceRoute']);
            $this->app['router']->delete('/email/hosted-routes/{id}', [EmailHostedMailController::class, 'deleteRoute']);
            $this->app['router']->get('/email/dkim-keys', [EmailDkimKeyController::class, 'index']);
            $this->app['router']->post('/email/dkim-keys', [EmailDkimKeyController::class, 'store']);
            $this->app['router']->get('/email/dkim-keys/{id}', [EmailDkimKeyController::class, 'show']);
            $this->app['router']->put('/email/dkim-keys/{id}', [EmailDkimKeyController::class, 'replace']);
            $this->app['router']->delete('/email/dkim-keys/{id}', [EmailDkimKeyController::class, 'destroy']);
            $this->app['router']->post('/email/submission-policy', [EmailSubmissionPolicyController::class, 'store']);
            $this->app['router']->put('/email/submission-policy', [EmailSubmissionPolicyController::class, 'replace']);
            $this->app['router']->delete('/email/submission-policy', [EmailSubmissionPolicyController::class, 'destroy']);
            $this->app['router']->get('/schedules/notification-destination', [ScheduleNotificationConfigController::class, 'show']);
            $this->app['router']->post('/schedules/notification-destination', [ScheduleNotificationConfigController::class, 'store']);
            $this->app['router']->put('/schedules/notification-destination', [ScheduleNotificationConfigController::class, 'replace']);
            $this->app['router']->delete('/schedules/notification-destination', [ScheduleNotificationConfigController::class, 'destroy']);
            $this->app['router']->get('/webstream/private-sources', [WebstreamPrivateSourceController::class, 'index']);
            $this->app['router']->post('/webstream/private-sources', [WebstreamPrivateSourceController::class, 'store']);
            $this->app['router']->get('/webstream/private-sources/{source}', [WebstreamPrivateSourceController::class, 'show']);
            $this->app['router']->put('/webstream/private-sources/{source}', [WebstreamPrivateSourceController::class, 'replace']);
            $this->app['router']->delete('/webstream/private-sources/{source}', [WebstreamPrivateSourceController::class, 'destroy']);
            $this->app['router']->get('/webstream/routes', [WebstreamRoutePolicyController::class, 'index']);
            $this->app['router']->post('/webstream/routes', [WebstreamRoutePolicyController::class, 'store']);
            $this->app['router']->get('/webstream/routes/{policy}', [WebstreamRoutePolicyController::class, 'show']);
            $this->app['router']->put('/webstream/routes/{policy}', [WebstreamRoutePolicyController::class, 'replace']);
            $this->app['router']->delete('/webstream/routes/{policy}', [WebstreamRoutePolicyController::class, 'destroy']);
            $this->app['router']->post('/deliveries/{delivery}/retry', [DeliveryController::class, 'retry']);
            $this->app['router']->get('/email/domains', [EmailController::class, 'domains']);
            $this->app['router']->post('/accounts', [AccountController::class, 'create']);
            $this->app['router']->post('/email/mailbox-accounts', [AccountController::class, 'create']);
            $this->app['router']->get('/email/receiving-settings', [AccountReceivingController::class, 'settings']);
            $this->app['router']->put('/email/receiving-settings', [AccountReceivingController::class, 'configure']);
            $this->app['router']->get('/email/mailbox-accounts/{account}/receiving-domains', [AccountReceivingController::class, 'domains']);
            $this->app['router']->post('/email/mailbox-accounts/{account}/receiving-domains', [AccountReceivingController::class, 'createDomain']);
            $this->app['router']->get('/email/mailbox-accounts/{account}/receiving-domains/{domain}', [AccountReceivingController::class, 'showDomain']);
            $this->app['router']->post('/email/mailbox-accounts/{account}/receiving-domains/{domain}/verify', [AccountReceivingController::class, 'verifyDomain']);
            $this->app['router']->get('/email/mailbox-accounts/{account}/receiving-domains/{domain}/migration-record', [AccountReceivingController::class, 'migrationRecord']);
            $this->app['router']->post('/email/mailbox-accounts/{account}/receiving-domains/{domain}/migrate', [AccountReceivingController::class, 'migrateDomain']);
            $this->app['router']->delete('/email/mailbox-accounts/{account}/receiving-domains/{domain}', [AccountReceivingController::class, 'deleteDomain']);
            $this->app['router']->get('/email/mailbox-accounts/{account}/receiving-routes', [AccountReceivingController::class, 'routes']);
            $this->app['router']->post('/email/mailbox-accounts/{account}/receiving-routes', [AccountReceivingController::class, 'createRoute']);
            $this->app['router']->get('/email/mailbox-accounts/{account}/receiving-routes/{route}', [AccountReceivingController::class, 'showRoute']);
            $this->app['router']->patch('/email/mailbox-accounts/{account}/receiving-routes/{route}', [AccountReceivingController::class, 'updateRoute']);
            $this->app['router']->delete('/email/mailbox-accounts/{account}/receiving-routes/{route}', [AccountReceivingController::class, 'deleteRoute']);
            $this->app['router']->get('/email/mailbox-accounts/{account}/sending-domains', [ManagedSendingController::class, 'domains']);
            $this->app['router']->post('/email/mailbox-accounts/{account}/sending-domains', [ManagedSendingController::class, 'createDomain']);
            $this->app['router']->get('/email/mailbox-accounts/{account}/sending-domains/{domain}', [ManagedSendingController::class, 'showDomain']);
            $this->app['router']->post('/email/mailbox-accounts/{account}/sending-domains/{domain}/verify', [ManagedSendingController::class, 'verifyDomain']);
            $this->app['router']->get('/email/mailbox-accounts/{account}/sending-domains/{domain}/migration-record', [ManagedSendingController::class, 'migrationRecord']);
            $this->app['router']->post('/email/mailbox-accounts/{account}/sending-domains/{domain}/migrate', [ManagedSendingController::class, 'migrateDomain']);
            $this->app['router']->delete('/email/mailbox-accounts/{account}/sending-domains/{domain}', [ManagedSendingController::class, 'deleteDomain']);
            $this->app['router']->get('/email/mailbox-accounts/{account}/sending-routes', [ManagedSendingController::class, 'routes']);
            $this->app['router']->post('/email/mailbox-accounts/{account}/sending-routes', [ManagedSendingController::class, 'createRoute']);
            $this->app['router']->get('/email/mailbox-accounts/{account}/sending-routes/{route}', [ManagedSendingController::class, 'showRoute']);
            $this->app['router']->patch('/email/mailbox-accounts/{account}/sending-routes/{route}', [ManagedSendingController::class, 'updateRoute']);
            $this->app['router']->post('/email/mailbox-accounts/{account}/sending-routes/{route}/rotate', [ManagedSendingController::class, 'rotate']);
            $this->app['router']->delete('/email/mailbox-accounts/{account}/sending-routes/{route}', [ManagedSendingController::class, 'deleteRoute']);
            $this->app['router']->get('/email/mailbox-accounts/{account}/receiving-routes/{route}/worker-ingress', [WorkerIngressController::class, 'show']);
            $this->app['router']->post('/email/mailbox-accounts/{account}/receiving-routes/{route}/worker-ingress', [WorkerIngressController::class, 'rotate']);
            $this->app['router']->post('/email/operator/managed-worker-ingress/{account}/{route}/rotate', [WorkerIngressController::class, 'operatorRotate']);
            $this->app['router']->get('/email/mailbox-accounts/{account}/sending-connections', [SendingConnectionController::class, 'index']);
            $this->app['router']->post('/email/mailbox-accounts/{account}/sending-connections', [SendingConnectionController::class, 'create']);
            $this->app['router']->get('/email/mailbox-accounts/{account}/sending-connections/{connection}', [SendingConnectionController::class, 'show']);
            $this->app['router']->post('/email/mailbox-accounts/{account}/sending-connections/{connection}/test', [SendingConnectionController::class, 'test']);
            $this->app['router']->post('/email/mailbox-accounts/{account}/sending-connections/{connection}/rotate', [SendingConnectionController::class, 'rotate']);
            $this->app['router']->patch('/email/mailbox-accounts/{account}/sending-connections/{connection}', [SendingConnectionController::class, 'status']);
            $this->app['router']->put('/email/mailbox-accounts/{account}/sending-connections/{connection}', [SendingConnectionController::class, 'replace']);
            $this->app['router']->delete('/email/mailbox-accounts/{account}/sending-connections/{connection}', [SendingConnectionController::class, 'delete']);
            $this->app['router']->get('/email/mailbox-accounts/{account}/imap-connections', [ImapConnectionController::class, 'index']);
            $this->app['router']->post('/email/mailbox-accounts/{account}/imap-connections', [ImapConnectionController::class, 'create']);
            $this->app['router']->get('/email/mailbox-accounts/{account}/imap-connections/{connection}', [ImapConnectionController::class, 'show']);
            $this->app['router']->post('/email/mailbox-accounts/{account}/imap-connections/{connection}/test', [ImapConnectionController::class, 'test']);
            $this->app['router']->post('/email/mailbox-accounts/{account}/imap-connections/{connection}/rotate', [ImapConnectionController::class, 'rotate']);
            $this->app['router']->patch('/email/mailbox-accounts/{account}/imap-connections/{connection}', [ImapConnectionController::class, 'status']);
            $this->app['router']->put('/email/mailbox-accounts/{account}/imap-connections/{connection}', [ImapConnectionController::class, 'replace']);
            $this->app['router']->delete('/email/mailbox-accounts/{account}/imap-connections/{connection}', [ImapConnectionController::class, 'delete']);
            $this->app['router']->get('/email/mailbox-accounts/{account}/imap-connections/{connection}/polling', [MailboxPollController::class, 'show']);
            $this->app['router']->put('/email/mailbox-accounts/{account}/imap-connections/{connection}/polling', [MailboxPollController::class, 'put']);
            $this->app['router']->delete('/email/mailbox-accounts/{account}/imap-connections/{connection}/polling', [MailboxPollController::class, 'delete']);
            $this->app['router']->get('/email/mailbox-accounts/{account}/connections', [MailboxConnectionController::class, 'index']);
            $this->app['router']->post('/email/mailbox-accounts/{account}/connections', [MailboxConnectionController::class, 'create']);
            $this->app['router']->get('/email/mailbox-accounts/{account}/connections/{connection}', [MailboxConnectionController::class, 'show']);
            $this->app['router']->post('/email/mailbox-accounts/{account}/connections/{connection}/test', [MailboxConnectionController::class, 'test']);
            $this->app['router']->patch('/email/mailbox-accounts/{account}/connections/{connection}', [MailboxConnectionController::class, 'status']);
            $this->app['router']->put('/email/mailbox-accounts/{account}/connections/{connection}', [MailboxConnectionController::class, 'replace']);
            $this->app['router']->delete('/email/mailbox-accounts/{account}/connections/{connection}', [MailboxConnectionController::class, 'delete']);
            $this->app['router']->get('/email/mailbox-accounts/{account}/connections/{connection}/polling', [MailboxPollController::class, 'show']);
            $this->app['router']->put('/email/mailbox-accounts/{account}/connections/{connection}/polling', [MailboxPollController::class, 'put']);
            $this->app['router']->delete('/email/mailbox-accounts/{account}/connections/{connection}/polling', [MailboxPollController::class, 'delete']);
            $this->app['router']->get('/email/hooks', [MailHookController::class, 'index']);
            $this->app['router']->post('/email/hooks', [MailHookController::class, 'create']);
            $this->app['router']->get('/email/hooks/{hook}', [MailHookController::class, 'show']);
            $this->app['router']->patch('/email/hooks/{hook}', [MailHookController::class, 'status']);
            $this->app['router']->delete('/email/hooks/{hook}', [MailHookController::class, 'delete']);
            $this->app['router']->post('/email/submissions', EmailSubmissionController::class);
            $this->app['router']->get('/email/suppressions', [EmailSuppressionController::class, 'index']);
            $this->app['router']->post('/email/suppressions', [EmailSuppressionController::class, 'store']);
            $this->app['router']->delete('/email/suppressions/{suppression}', [EmailSuppressionController::class, 'destroy']);
            $this->app['router']->post('/email/domains', [EmailController::class, 'createDomain']);
            $this->app['router']->get('/email/domains/{domain}', [EmailController::class, 'showDomain']);
            $this->app['router']->post('/email/domains/{domain}/verify', [EmailController::class, 'verifyDomain']);
            $this->app['router']->get('/email/domains/{domain}/migration-record', [EmailController::class, 'migrationRecord']);
            $this->app['router']->post('/email/domains/{domain}/migrate', [EmailController::class, 'migrateDomain']);
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
            $this->app['router']->get('/dns/zones', [DnsZoneController::class, 'index'])->name('dns.zones.index');
            $this->app['router']->post('/dns/zones', [DnsZoneController::class, 'store'])->name('dns.zones.store');
            $this->app['router']->get('/dns/zones/{zone}', [DnsZoneController::class, 'show'])->name('dns.zones.show');
            $this->app['router']->put('/dns/zones/{zone}/records', [DnsZoneController::class, 'replaceRecords'])->name('dns.zones.records');
            $this->app['router']->post('/dns/zones/{zone}/provision', [DnsZoneController::class, 'provision'])->name('dns.zones.provision');
            $this->app['router']->delete('/dns/zones/{zone}', [DnsZoneController::class, 'destroy'])->name('dns.zones.destroy');
            $this->app['router']->get('/dns/hooks', [DnsHookController::class, 'index'])->name('dns.hooks.index');
            $this->app['router']->post('/dns/hooks', [DnsHookController::class, 'store'])->name('dns.hooks.store');
            $this->app['router']->get('/dns/hooks/{hook}', [DnsHookController::class, 'show'])->name('dns.hooks.show');
            $this->app['router']->patch('/dns/hooks/{hook}', [DnsHookController::class, 'update'])->name('dns.hooks.update');
            $this->app['router']->delete('/dns/hooks/{hook}', [DnsHookController::class, 'destroy'])->name('dns.hooks.destroy');
            $this->app['router']->get('/dns/hooks/{hook}/events', DnsHookEventController::class)->name('dns.hooks.events');
            $this->app['router']->get('/dns/hooks/{hook}/usage', DnsHookUsageController::class)->name('dns.hooks.usage');
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
                ->post('/registry/endpoints/{endpointKey}/subscriptions/{destinationId}/ssh/rotate-key', [SubscriptionManagementController::class, 'rotateSshKey'])
                ->name('registry.subscriptions.ssh.rotate-key');
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
