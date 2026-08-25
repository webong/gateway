<?php

declare(strict_types=1);

namespace Webong\WebRelay;

use Webong\WebProxy\DestinationRecord;
use Webong\WebProxy\WebProxyChannelManager;
use Webong\WebProxy\WebProxyRegistryManager;
use Webong\WebProxy\WebhookRoute;
use Webong\WebRelay\Exceptions\EndpointNotFoundException;

final readonly class SubscribeEndpoint
{
    public function __construct(
        private WebProxyRegistryManager $registryManager,
        private WebProxyChannelManager $channelManager,
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
                ($destination->metadata['delivery_mode'] ?? 'relay') === 'reply'
                && ($destination->owner_id !== $subscription->subscriberId
                    || $destination->registration_id !== $subscription->subscriptionId));

            if ($reply !== null) {
                throw new \InvalidArgumentException('This route already has a synchronous reply subscription.');
            }
        }

        return $resolved->endpoint->attach($subscription->toDestinationDefinition());
    }
}
