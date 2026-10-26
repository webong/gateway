<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Illuminate\Support\ServiceProvider;
use RuntimeException;
use Webong\Gateway\Accounts\AccountStore;
use Webong\Gateway\Automation\CapabilityAwareRoutePlanner;
use Webong\Gateway\Companion\ExtensionRuntimeConfigurationRegistry;
use Webong\Gateway\Contracts\AutomationLanguageRegistry;
use Webong\Gateway\Contracts\CacheablePathResolver;
use Webong\Gateway\Contracts\PathResolver;
use Webong\Gateway\Deliveries\DeliveryAdminClient;
use Webong\Gateway\GatewayRouter;
use Webong\Gateway\Planning\PathResolverRegistry;
use Webong\Gateway\Planning\ProtocolPlannerRegistry;
use Webong\Gateway\Protocols\Protocol;
use Webong\Gateway\Resources\ConfigurationKindRegistry;
use Webong\Gateway\Resources\PlannerResourceStore;
use Webong\Gateway\Schedules\NativeScheduleJobRegistry;
use Webong\Proxy\WebProxy;

final class EmailServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $this->mergeConfigFrom(__DIR__.'/../config/email.php', 'gateway.email');

        $this->app->bind(TxtResolver::class, NativeTxtResolver::class);
        $this->app->bind(MxResolver::class, NativeMxResolver::class);
        $this->app->bind(ApplicationMailboxGrantSource::class, ConfiguredApplicationMailboxGrantSource::class);
        $this->app->singleton(CredentialScriptRegistry::class, fn (): CredentialScriptRegistry => new CredentialScriptRegistry(
            $this->app->make(AutomationLanguageRegistry::class),
            $this->app->make(DeliveryAdminClient::class),
        ));
        $this->app->bind(MailboxConnectionStore::class, function (): MailboxConnectionStore {
            $configured = config('gateway.email.oauth_refresher');
            $refresher = null;
            if (is_string($configured) && $configured !== '') {
                $refresher = $this->app->make($configured);
                if (! $refresher instanceof MailboxOAuthRefresher) {
                    throw new RuntimeException('Gateway mailbox OAuth refresher must implement MailboxOAuthRefresher.');
                }
            }

            return new MailboxConnectionStore(
                $refresher,
                $this->app->make(EmailUpstreamCatalog::class),
                $this->app->make(PlannerResourceStore::class),
                $this->app->make(AccountStore::class),
                $this->app->make(ApplicationMailboxGrantSource::class),
                $this->app->make(MailboxCredentialSourceStore::class),
            );
        });
        $this->app->bind(SrsReturnStore::class, fn (): SrsReturnStore => new SrsReturnStore(
            $this->app->make(EmailSrsPolicyStore::class),
            $this->app->make(EmailRegistry::class),
        ));
        $this->app->bind(EmailProtocolPlanner::class, fn (): EmailProtocolPlanner => new EmailProtocolPlanner(
            routePlanner: $this->app->make(CapabilityAwareRoutePlanner::class),
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
            outbound: $this->app->make(OutboundTransportStore::class),
            providerRoutes: $this->emailProviderRouteResolver(),
        ));

        $this->app->afterResolving(ConfigurationKindRegistry::class,
            static fn (ConfigurationKindRegistry $kinds) => $kinds->register(
                'email.submission-policy', 'email.runtime-policy', 'email.verification-profile',
                'email.srs-policy', 'email.upstream', 'email.hosted-adapter', 'email.hosted-route',
                'email.dkim-key', 'email.domain', 'email.alias', 'email.receiving-domain',
                'email.receiving-route', 'email.receiving-settings', 'email.sending-domain',
                'email.sending-route', 'email.delivery-status-hook',
            ));
        $this->app->afterResolving(ProtocolPlannerRegistry::class, function (ProtocolPlannerRegistry $planners): void {
            $factory = fn (): EmailProtocolPlanner => $this->app->make(EmailProtocolPlanner::class);
            $planners->register(Protocol::SMTP, $factory);
            $planners->register(Protocol::IMAP, $factory);
            $planners->register(Protocol::POP3, $factory);
        });
        $this->app->afterResolving(PathResolverRegistry::class, function (PathResolverRegistry $resolvers): void {
            $resolvers->register('email', function (?PathResolver $fallback): PathResolver {
                $hooks = $this->app->make(MailHookRegistry::class);

                return $fallback === null || $fallback instanceof CacheablePathResolver
                    ? new CacheableMailHookPathResolver($hooks, $fallback)
                    : new MailHookPathResolver($hooks, $fallback);
            });
        });
        $this->app->afterResolving(NativeScheduleJobRegistry::class, function (NativeScheduleJobRegistry $jobs): void {
            $jobs->registerJob('imap', 'native.imap');
            $jobs->registerNotification('email_delivery_status',
                fn (): array => $this->app->make(EmailDeliveryStatusDispatcher::class)->dispatch());
        });
        $this->app->afterResolving(ExtensionRuntimeConfigurationRegistry::class,
            static function (ExtensionRuntimeConfigurationRegistry $runtime): void {
                $runtime->register('webong/email', static function (array $configuration): array {
                    $allowed = config('gateway.email.hosted_allowed_hosts', []);
                    if (! is_array($allowed) || $allowed === []) {
                        return $configuration;
                    }
                    $hosted = is_array($configuration['hosted_mail'] ?? null) ? $configuration['hosted_mail'] : [];
                    $hosted['allowed_hosts'] = array_values(array_unique($allowed));
                    $configuration['hosted_mail'] = $hosted;

                    return $configuration;
                });
            });
    }

    public function boot(): void
    {
        $client = trim((string) config('gateway.email.hook_client', 'gateway'));
        if ($client === '') {
            throw new RuntimeException('Gateway mail hooks require gateway.email.hook_client.');
        }
        $proxy = $this->app->make(WebProxy::class);
        if (! $proxy->has($client)) {
            $proxy->register($client, GatewayRouter::class);
        }

        $this->loadMigrationsFrom(__DIR__.'/../database/migrations');
        $this->loadRoutesFrom(__DIR__.'/../routes/planner.php');
        if ($this->app->runningInConsole()) {
            $this->commands([PruneEmailSubmissionUsageCommand::class, SendEmailDeliveryStatusEventsCommand::class]);
        }
        $this->publishes([__DIR__.'/../config/email.php' => config_path('gateway-email.php')], 'gateway-email-config');
    }

    private function emailMessagePolicy(): ?EmailMessagePolicy
    {
        return $this->configuredPolicy('policy', EmailMessagePolicy::class);
    }

    private function emailSubmissionAuthenticator(): ?EmailSubmissionAuthenticator
    {
        return $this->configuredPolicy('submission_authenticator', EmailSubmissionAuthenticator::class);
    }

    private function mailboxAuthenticator(): ?MailboxAuthenticator
    {
        return $this->configuredPolicy('mailbox_authenticator', MailboxAuthenticator::class);
    }

    /** @template T of object @param class-string<T> $contract @return T|null */
    private function configuredPolicy(string $key, string $contract): ?object
    {
        $configured = config('gateway.email.'.$key);
        if ($configured === null || $configured === '') {
            return null;
        }
        if (! is_string($configured)) {
            throw new RuntimeException("Gateway Email {$key} must be a configured class name.");
        }
        $policy = $this->app->make($configured);
        if (! $policy instanceof $contract) {
            throw new RuntimeException("Gateway Email {$key} [{$configured}] must implement {$contract}.");
        }

        return $policy;
    }

    private function emailProviderRouteResolver(): EmailProviderRouteResolver
    {
        $configured = config('gateway.email.provider_route_resolver');
        $resolver = null;
        if ($configured !== null && $configured !== '') {
            if (! is_string($configured)) {
                throw new RuntimeException('Gateway Email provider route resolver must be a class name.');
            }
            $resolver = $this->app->make($configured);
            if (! $resolver instanceof EmailProviderRouteResolver) {
                throw new RuntimeException('Gateway Email provider route resolver must implement EmailProviderRouteResolver.');
            }
        }

        return new PlannerHostedMailRouteResolver($this->app->make(EmailHostedMailCatalog::class), $resolver);
    }
}
