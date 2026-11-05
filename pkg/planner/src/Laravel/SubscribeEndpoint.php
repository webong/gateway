<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel;

use Webong\Gateway\Exceptions\EndpointNotFoundException;
use Webong\Gateway\Protocols\SubscriptionState;
use Webong\Proxy\DestinationRecord;
use Webong\Proxy\WebProxyChannelManager;
use Webong\Proxy\WebProxyRegistryManager;
use Webong\Proxy\WebhookRoute;

final readonly class SubscribeEndpoint
{
    public function __construct(
        private WebProxyRegistryManager $registryManager,
        private WebProxyChannelManager $channelManager,
        private SubscriptionTargetExtensionRegistry $targetExtensions,
        private ?RegistryRouteCache $routeCache = null,
    ) {}

    public function handle(string $endpointKey, SubscriptionDefinition $subscription): DestinationRecord
    {
        $extension = $this->targetExtensions->forKind($subscription->targetKind);
        $extension->assertDefinition($subscription);
        $resolved = $this->registryManager->resolveByKey($endpointKey, $this->channelManager->registries($subscription->channel));
        if ($resolved === null) { throw new EndpointNotFoundException("Registered endpoint [{$endpointKey}] was not found."); }

        $extension->assertAllowed($resolved->endpoint->record->endpoint_key, $subscription->subscriberId,
            $subscription->target, $subscription->targetOptions, $subscription->metadata, $resolved->endpoint);

        if ($subscription->type === 'reply') {
            $reply = $resolved->registrar->provider()->destinationsFor($resolved->endpoint->record, new WebhookRoute(
                scope: $subscription->routingScope, key: $subscription->routingKey, payload: [],
            ))->first(function (DestinationRecord $destination) use ($subscription, $extension): bool {
                if (SubscriptionState::status($destination->metadata) !== SubscriptionState::ACTIVE
                    || ($destination->metadata['delivery_mode'] ?? 'relay') !== 'reply') { return false; }
                $sameIdentity = $destination->owner_id === $subscription->subscriberId
                    && $destination->registration_id === $subscription->subscriptionId;
                return ! $sameIdentity || ! $extension->canReuseReply($subscription, $destination);
            });
            if ($reply !== null) { throw new \InvalidArgumentException('This route already has a synchronous reply subscription.'); }
        }

        $destination = $resolved->endpoint->attach($subscription->toDestinationDefinition());
        $this->routeCache?->invalidateEndpoint($resolved->endpoint->record->endpoint_key);
        return $destination;
    }
}
