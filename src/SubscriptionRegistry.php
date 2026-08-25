<?php

declare(strict_types=1);

namespace Webong\WebRelay;

use Closure;
use Illuminate\Contracts\Cache\Factory as CacheFactory;
use Illuminate\Contracts\Cache\LockProvider;
use Illuminate\Contracts\Cache\LockTimeoutException;
use InvalidArgumentException;
use Webong\WebProxy\DestinationRecord;
use Webong\WebProxy\Enums\WebhookProxyTargetType;
use Webong\WebProxy\WebProxyChannelManager;
use Webong\WebProxy\WebProxyRegistryManager;
use Webong\WebProxy\WebhookRoute;
use Webong\WebRelay\Exceptions\EndpointNotFoundException;
use Webong\WebRelay\Exceptions\SubscriptionLockUnavailableException;
use Webong\WebRelay\Exceptions\SubscriptionNotFoundException;
use Webong\WebRelay\Exceptions\SubscriptionRevisionMismatchException;
use Webong\WebRelay\Protocol\SubscriptionState;

final readonly class SubscriptionRegistry
{
    public function __construct(
        private WebProxyRegistryManager $registryManager,
        private WebProxyChannelManager $channelManager,
        private CacheFactory $cache,
        private RegistryRouteCache $routeCache,
    ) {
    }

    /** @return list<SubscriptionResource> */
    public function route(
        string $endpointKey,
        string $routingScope,
        string $routingKey,
        ?string $channel = null,
        bool $includeRemoved = false,
    ): array {
        $resolved = $this->endpoint($endpointKey, $channel);

        return $resolved->provider->destinationsFor(
            $resolved->endpoint->record,
            new WebhookRoute(scope: $routingScope, key: $routingKey, payload: []),
        )
            ->filter(static fn (DestinationRecord $destination): bool =>
                $destination->target_type === WebhookProxyTargetType::REQUEST
                && ($includeRemoved || SubscriptionState::status($destination->metadata) !== SubscriptionState::REMOVED))
            ->map(static fn (DestinationRecord $destination): SubscriptionResource =>
                new SubscriptionResource($resolved->endpoint->record->endpoint_key, $destination))
            ->values()
            ->all();
    }

    public function get(string $endpointKey, string $destinationId, ?string $channel = null): SubscriptionResource
    {
        $resolved = $this->find($endpointKey, $destinationId, $channel);

        return new SubscriptionResource($resolved->endpoint->record->endpoint_key, $resolved->destination);
    }

    /**
     * @param Closure(ResolvedSubscription): array<string, mixed> $mutation
     */
    public function mutate(
        string $endpointKey,
        string $destinationId,
        string $expectedRevision,
        Closure $mutation,
        ?string $channel = null,
    ): SubscriptionResource {
        $initial = $this->find($endpointKey, $destinationId, $channel);
        $lockName = implode(':', [
            (string) config('web-relay.cache.prefix', 'web-relay'),
            'subscription-route-lock',
            hash('sha256', implode("\0", [
                $initial->endpoint->record->endpoint_key,
                $initial->destination->routing_scope,
                $initial->destination->routing_key,
            ])),
        ]);
        $store = config('web-relay.mutations.lock_store', config('web-relay.cache.store'));
        $repository = $this->cache->store(is_string($store) && $store !== '' ? $store : null);
        $lockProvider = method_exists($repository, 'getStore') ? $repository->getStore() : null;

        if (! $lockProvider instanceof LockProvider) {
            throw new SubscriptionLockUnavailableException('The configured cache store does not support subscription locks.');
        }

        $lock = $lockProvider->lock(
            $lockName,
            max(1, (int) config('web-relay.mutations.lock_seconds', 10)),
        );

        try {
            return $lock->block(
                max(0, (int) config('web-relay.mutations.lock_wait_seconds', 5)),
                function () use ($endpointKey, $destinationId, $expectedRevision, $mutation, $channel): SubscriptionResource {
                    $resolved = $this->find($endpointKey, $destinationId, $channel);
                    $currentRevision = SubscriptionState::revision($resolved->destination->metadata);
                    if (! hash_equals($currentRevision, $expectedRevision)) {
                        throw new SubscriptionRevisionMismatchException($currentRevision);
                    }

                    $attributes = $mutation($resolved);
                    if (! is_array($attributes)) {
                        throw new InvalidArgumentException('Subscription mutations must return destination attributes.');
                    }

                    $metadata = $attributes['metadata'] ?? $resolved->destination->metadata;
                    if (! is_array($metadata)) {
                        throw new InvalidArgumentException('Subscription metadata must be an object.');
                    }
                    $metadata[SubscriptionState::REVISION_METADATA_KEY] = SubscriptionState::newRevision();
                    $attributes['metadata'] = $metadata;

                    $context = $resolved->destination->metadata[DestinationRecord::EXECUTION_CONTEXT_METADATA_KEY] ?? [];
                    $resolved->provider->run(
                        fn (): mixed => $resolved->provider->updateDestination($resolved->destination, $attributes),
                        is_array($context) ? $context : [],
                    );
                    $this->routeCache->invalidateEndpoint($resolved->endpoint->record->endpoint_key);

                    return $this->get($endpointKey, $destinationId, $channel);
                },
            );
        } catch (LockTimeoutException) {
            throw new SubscriptionLockUnavailableException('The subscription is busy; retry the request.');
        }
    }

    public function assertReplyAvailable(ResolvedSubscription $resolved): void
    {
        $reply = $resolved->provider->destinationsFor(
            $resolved->endpoint->record,
            new WebhookRoute(
                scope: $resolved->destination->routing_scope,
                key: $resolved->destination->routing_key,
                payload: [],
            ),
        )->first(static fn (DestinationRecord $candidate): bool =>
            $candidate->id !== $resolved->destination->id
            && SubscriptionState::status($candidate->metadata) === SubscriptionState::ACTIVE
            && ($candidate->metadata['delivery_mode'] ?? 'relay') === 'reply');

        if ($reply !== null) {
            throw new InvalidArgumentException('This route already has a synchronous reply subscription.');
        }
    }

    private function find(string $endpointKey, string $destinationId, ?string $channel): ResolvedSubscription
    {
        $resolved = $this->endpoint($endpointKey, $channel);
        $destination = $resolved->provider->destinationById($destinationId);

        if ($destination === null || $destination->endpoint_id !== $resolved->endpoint->record->id) {
            throw new SubscriptionNotFoundException("Subscription destination [{$destinationId}] was not found.");
        }

        return new ResolvedSubscription($resolved->endpoint, $resolved->provider, $destination);
    }

    private function endpoint(string $endpointKey, ?string $channel): ResolvedRegistryEndpoint
    {
        $resolved = $this->registryManager->resolveByKey(
            $endpointKey,
            $this->channelManager->registries($channel),
        );

        if ($resolved === null) {
            throw new EndpointNotFoundException("Registered endpoint [{$endpointKey}] was not found.");
        }

        return new ResolvedRegistryEndpoint(
            $resolved->endpoint,
            $resolved->registrar->provider(),
        );
    }
}
