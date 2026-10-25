<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Illuminate\Contracts\Cache\Factory as CacheFactory;
use Illuminate\Http\Client\Factory as HttpFactory;
use Illuminate\Support\ServiceProvider;
use RuntimeException;
use Webong\Gateway\Accounts\Http\AccountController;
use Webong\Gateway\Automation\CapabilityAwareRoutePlanner;
use Webong\Gateway\Automation\ExtAutomationPackageCatalog;
use Webong\Gateway\Automation\InMemoryAutomationLanguageRegistry;
use Webong\Gateway\Companion\CompanionActionActivationStore;
use Webong\Gateway\Companion\CompanionArtifactReconciler;
use Webong\Gateway\Companion\CompanionActionExecutionStore;
use Webong\Gateway\Companion\ExtCompanionPackageCatalog;
use Webong\Gateway\Companion\ExtensionArtifactInstaller;
use Webong\Gateway\Companion\ExtensionArtifactReconciler;
use Webong\Gateway\Companion\ExtensionRuntimeConfigurationRegistry;
use Webong\Gateway\Companion\Http\CompanionExtensionController;
use Webong\Gateway\Companion\Http\InternalExtensionDistributionController;
use Webong\Gateway\Companion\Http\InternalCompanionActionController;
use Webong\Gateway\Companion\PruneCompanionActionExecutionsCommand;
use Webong\Gateway\Companion\RegisteredCompanionActions;
use Webong\Gateway\Contracts\AutomationCapability;
use Webong\Gateway\Contracts\AutomationExecutionTarget;
use Webong\Gateway\Contracts\AutomationLanguageRegistry;
use Webong\Gateway\Contracts\CompanionActionRegistry;
use Webong\Gateway\Contracts\ExtensionProviderRegistry;
use Webong\Gateway\Contracts\PathResolver;
use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Contracts\ScriptScheduleRegistrar;
use Webong\Gateway\Deliveries\Http\DeliveryController;
use Webong\Gateway\Events\PruneEventsCommand;
use Webong\Gateway\Planning\PathResolverRegistry;
use Webong\Gateway\Planning\ProtocolPlannerRegistry;
use Webong\Gateway\Planning\RoutePlanObserverRegistry;
use Webong\Gateway\Reconciliation\Http\Controllers\InternalInstanceController;
use Webong\Gateway\Reconciliation\Http\Controllers\InternalRouteController;
use Webong\Gateway\Reconciliation\Http\Controllers\InternalAssignmentController;
use Webong\Gateway\Reconciliation\Http\Controllers\InternalServerController;
use Webong\Gateway\Reconciliation\Http\Controllers\InternalTargetController;
use Webong\Gateway\Reconciliation\InternalRequestAuthenticator;
use Webong\Gateway\Resources\Http\ConfigurationController;
use Webong\Gateway\Resources\ConfigurationKindRegistry;
use Webong\Gateway\Resources\PlannerResourceStore;
use Webong\Gateway\Resources\Http\ShellHookGrantController;
use Webong\Gateway\Schedules\Http\InternalScheduleController;
use Webong\Gateway\Schedules\Http\ScheduleController;
use Webong\Gateway\Schedules\Http\ScheduleNotificationConfigController;
use Webong\Gateway\Schedules\Http\ScheduleDeliveryOutcomeController;
use Webong\Gateway\Schedules\NativeScheduleJobRegistry;
use Webong\Gateway\Schedules\PersistScriptSchedules;
use Webong\Gateway\Schedules\SendScheduleNotificationsCommand;
use Webong\Gateway\Servers\Http\Controllers\ApplicationController as ManagedApplicationController;
use Webong\Gateway\Servers\Http\Controllers\InstanceController as ManagedInstanceController;
use Webong\Gateway\Servers\Http\Controllers\ServerController as ManagedServerController;
use Webong\Gateway\Servers\Http\Controllers\ServerLifecycleController;
use Webong\Gateway\Servers\Http\Controllers\TargetController;
use Webong\Gateway\Servers\ServerRegistry;
use Webong\Gateway\Servers\ServerTypeRegistry;

final class GatewayServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $this->mergeConfigFrom(dirname(__DIR__).'/config/gateway.php', 'gateway');

        $this->app->singleton(CompanionActionActivationStore::class);
        $this->app->singleton(ExtCompanionPackageCatalog::class, fn (): ExtCompanionPackageCatalog => new ExtCompanionPackageCatalog(
            root: (string) config('gateway.companion_actions.plugin_dir', ''),
            maxRuntimeSeconds: max(1, (int) config('gateway.companion_actions.max_runtime_seconds', 300)),
            maxOutputBytes: max(1, (int) config('gateway.companion_actions.max_output_bytes', 1048576)),
            phpBinary: (string) config('gateway.companion_actions.php_binary', PHP_BINARY),
            executionPolicy: fn (string $extension) => $this->app
                ->make(CompanionActionActivationStore::class)
                ->selection($extension)
                ->execution,
            generationRoot: (string) config('gateway.companion_actions.generation_root', storage_path('framework/cache/gateway/companion-generations')),
        ));
        $this->app->singleton(RegisteredCompanionActions::class);
        $this->app->alias(RegisteredCompanionActions::class, CompanionActionRegistry::class);
        $this->app->singleton(ExtensionArtifactInstaller::class, fn (): ExtensionArtifactInstaller => new ExtensionArtifactInstaller(
            (string) config('gateway.companion_actions.plugin_dir', ''),
            $this->app->make(HttpFactory::class),
        ));
        $this->app->singleton(ExtensionArtifactReconciler::class);
        $this->app->singleton(ExtAutomationPackageCatalog::class, fn (): ExtAutomationPackageCatalog => new ExtAutomationPackageCatalog(
            (string) config('gateway.companion_actions.plugin_dir', ''),
            $this->app->make(ExtensionArtifactReconciler::class),
        ));
        $this->app->singleton(CompanionArtifactReconciler::class);
        $this->app->singleton(CompanionActionExecutionStore::class);
        $this->app->singleton(ExtensionRuntimeConfigurationRegistry::class);

        foreach (ExtensionProviderRegistry::providers() as $provider) {
            $this->app->register($provider);
        }

        $this->app->singleton(ServerTypeRegistry::class);
        $this->app->singleton(ServerRegistry::class);
        $this->app->singleton(ConfigurationKindRegistry::class);
        $this->app->singleton(PathResolverRegistry::class);
        $this->app->singleton(ProtocolPlannerRegistry::class);
        $this->app->singleton(RoutePlanObserverRegistry::class);
        $this->app->singleton(NativeScheduleJobRegistry::class);
        $this->app->bind(ScriptScheduleRegistrar::class, PersistScriptSchedules::class);
        $this->app->singleton(InternalRequestAuthenticator::class);
        $this->app->singleton(AutomationLanguageRegistry::class, function (): AutomationLanguageRegistry {
            $registry = new InMemoryAutomationLanguageRegistry(
                fn (): array => $this->app->make(ExtAutomationPackageCatalog::class)->languages(),
            );
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
            if (is_string($resolver) && $resolver !== '') {
                $fallback = $this->app->make($resolver);
                if (! $fallback instanceof PathResolver) {
                    throw new RuntimeException("Gateway path resolver [{$resolver}] must implement PathResolver.");
                }
            }

            return $this->app->make(PathResolverRegistry::class)->decorate($fallback)
                ?? throw new RuntimeException('Configure gateway.path_resolver or install a Planner Extension that provides one.');
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
            observers: $this->app->make(RoutePlanObserverRegistry::class),
        ));

        $this->app->bind(RegistryProtocolPlanner::class, fn (): RegistryProtocolPlanner => new RegistryProtocolPlanner(
            planners: $this->app->make(ProtocolPlannerRegistry::class),
            fallback: new RouteProtocolPlanner($this->app->make(CapabilityAwareRoutePlanner::class)),
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

    public function boot(): void
    {
        $this->loadMigrationsFrom(__DIR__.'/database/migrations');
        if ($this->app->runningInConsole()) {
            $this->commands([
                PruneEventsCommand::class,
                PruneCompanionActionExecutionsCommand::class,
                SendScheduleNotificationsCommand::class,
            ]);
        }

        $this->publishes([
            dirname(__DIR__).'/config/gateway.php' => config_path('gateway.php'),
        ], 'gateway-config');

        $this->app->booted(function (): void {
            $companions = $this->app->make(RegisteredCompanionActions::class);
            $companions->replaceDynamicProviders($this->app->make(ExtCompanionPackageCatalog::class)->providers());

            $this->app['router']->get('/_internal/gateway/ready', InternalReadinessController::class);
            $this->app['router']->get('/_internal/gateway/shell/executions/{execution}', ShellSshExecutionController::class);
            $this->app['router']->post('/_internal/gateway/plan', PlanController::class);
            $this->app['router']->post('/_internal/gateway/event', ProtocolPlanController::class);
            $this->app['router']->get('/_internal/gateway/companion-actions', [InternalCompanionActionController::class, 'index']);
            $this->app['router']->post('/_internal/gateway/companion-actions/{action}/executions', [InternalCompanionActionController::class, 'execute']);
            $this->app['router']->get('/_internal/gateway/extensions', InternalExtensionDistributionController::class);
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
            $this->app['router']->get('/configurations/kinds', [ConfigurationController::class, 'kinds']);
            $this->app['router']->get('/configurations/{kind}/{id}/revisions', [ConfigurationController::class, 'revisions']);
            $this->app['router']->get('/extensions', [CompanionExtensionController::class, 'index']);
            $this->app['router']->post('/extensions', [CompanionExtensionController::class, 'store']);
            $this->app['router']->get('/extensions/{extension}', [CompanionExtensionController::class, 'show']);
            $this->app['router']->put('/extensions/{extension}', [CompanionExtensionController::class, 'replace']);
            $this->app['router']->delete('/extensions/{extension}', [CompanionExtensionController::class, 'destroy']);
            $this->app['router']->get('/shell/hook-grants', [ShellHookGrantController::class, 'index']);
            $this->app['router']->post('/shell/hook-grants', [ShellHookGrantController::class, 'store']);
            $this->app['router']->get('/shell/hook-grants/{grant}', [ShellHookGrantController::class, 'show']);
            $this->app['router']->put('/shell/hook-grants/{grant}', [ShellHookGrantController::class, 'replace']);
            $this->app['router']->delete('/shell/hook-grants/{grant}', [ShellHookGrantController::class, 'destroy']);
            $this->app['router']->get('/schedules/notification-destination', [ScheduleNotificationConfigController::class, 'show']);
            $this->app['router']->post('/schedules/notification-destination', [ScheduleNotificationConfigController::class, 'store']);
            $this->app['router']->put('/schedules/notification-destination', [ScheduleNotificationConfigController::class, 'replace']);
            $this->app['router']->delete('/schedules/notification-destination', [ScheduleNotificationConfigController::class, 'destroy']);
            $this->app['router']->post('/deliveries/{delivery}/retry', [DeliveryController::class, 'retry']);
            $this->app['router']->post('/accounts', [AccountController::class, 'create']);
            $this->app['router']->get('/schedules', [ScheduleController::class, 'index']);
            $this->app['router']->post('/schedules', [ScheduleController::class, 'store']);
            $this->app['router']->get('/schedules/{schedule}', [ScheduleController::class, 'show']);
            $this->app['router']->patch('/schedules/{schedule}', [ScheduleController::class, 'update']);
            $this->app['router']->delete('/schedules/{schedule}', [ScheduleController::class, 'destroy']);
            $this->app['router']->get('/schedules/{schedule}/runs', [ScheduleController::class, 'runs']);
            $this->app['router']->get('/schedules/{schedule}/runs/{run}/outcome', ScheduleDeliveryOutcomeController::class);
            $this->app['router']->post('/schedules/{schedule}/runs', [ScheduleController::class, 'run']);
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
