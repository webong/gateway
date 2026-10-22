<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Webong\Proxy\DestinationRecord;
use Webong\Proxy\WebProxyChannelManager;
use Webong\Proxy\WebProxyRegistryManager;
use Webong\Proxy\WebhookRoute;
use Webong\Gateway\Exceptions\EndpointNotFoundException;
use Webong\Gateway\Contracts\AutomationCapability;
use Webong\Gateway\Contracts\AutomationLanguageRegistry;
use Webong\Gateway\Protocol\SubscriptionState;

final readonly class SubscribeEndpoint
{
    public function __construct(
        private WebProxyRegistryManager $registryManager,
        private WebProxyChannelManager $channelManager,
        private EndpointHttpTransportResolver $transportResolver,
        private AutomationLanguageRegistry $languages,
        private ShellSubscriptionPolicy $shellPolicy,
        private ShellSshExecutionStore $sshExecutions,
        private ?RegistryRouteCache $routeCache = null,
    ) {
    }

    public function handle(string $endpointKey, SubscriptionDefinition $subscription): DestinationRecord
    {
        if ($subscription->shellHook !== null && ! $this->languages->supports('shell', AutomationCapability::ROUTE)) {
            throw new \InvalidArgumentException('The Shell route extension is not enabled.');
        }

        $resolved = $this->registryManager->resolveByKey(
            $endpointKey,
            $this->channelManager->registries($subscription->channel),
        );

        if ($resolved === null) {
            throw new EndpointNotFoundException("Registered endpoint [{$endpointKey}] was not found.");
        }

        if ($subscription->shellHook !== null) {
            $sshIdentity = $subscription->shellSshExecutionId === null ? null
                : $this->sshExecutions->publicIdentity($subscription->shellSshExecutionId);
            if ($subscription->shellSshExecutionId !== null && $sshIdentity === null) {
                throw new \InvalidArgumentException('The SSH Shell execution is unavailable.');
            }
            $this->shellPolicy->assertAllowed(
                $resolved->endpoint->record->endpoint_key,
                $subscription->subscriberId,
                $subscription->shellHook,
                $sshIdentity,
            );
        }

        if ($subscription->profile !== null) {
            $this->transportResolver->resolve($resolved->endpoint, $subscription->profile);
        }

        if ($subscription->type === 'reply') {
            $reply = $resolved->registrar->provider()->destinationsFor(
                $resolved->endpoint->record,
                new WebhookRoute(
                    scope: $subscription->routingScope,
                    key: $subscription->routingKey,
                    payload: [],
                ),
            )->first(static fn (DestinationRecord $destination): bool =>
                SubscriptionState::status($destination->metadata) === SubscriptionState::ACTIVE
                && ($destination->metadata['delivery_mode'] ?? 'relay') === 'reply'
                && ($subscription->shellSshExecutionId !== null
                    || ShellSubscriptionTarget::sshId($destination->target) !== null
                    || $destination->owner_id !== $subscription->subscriberId
                    || $destination->registration_id !== $subscription->subscriptionId));

            if ($reply !== null) {
                throw new \InvalidArgumentException('This route already has a synchronous reply subscription.');
            }
        }

        $destination = $resolved->endpoint->attach($subscription->toDestinationDefinition());
        $this->routeCache?->invalidateEndpoint($resolved->endpoint->record->endpoint_key);

        return $destination;
    }
}
