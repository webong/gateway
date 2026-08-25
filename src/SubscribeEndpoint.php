<?php

declare(strict_types=1);

namespace Webong\WebRelay;

use Webong\WebProxy\DestinationRecord;
use Webong\WebProxy\WebProxyChannelManager;
use Webong\WebProxy\WebProxyRegistryManager;
use Webong\WebProxy\WebhookRoute;
use Webong\WebRelay\Exceptions\EndpointNotFoundException;
use Webong\WebRelay\Protocol\SubscriptionState;

final readonly class SubscribeEndpoint
{
    public function __construct(
        private WebProxyRegistryManager $registryManager,
        private WebProxyChannelManager $channelManager,
        private ?RegistryRouteCache $routeCache = null,
    ) {
    }

    public function handle(string $endpointKey, SubscriptionDefinition $subscription): DestinationRecord
    {
        $resolved = $this->registryManager->resolveByKey(
            $endpointKey,
            $this->channelManager->registries($subscription->channel),
        );

        if ($resolved === null) {
            throw new EndpointNotFoundException("Registered endpoint [{$endpointKey}] was not found.");
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
                && ($destination->owner_id !== $subscription->subscriberId
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
