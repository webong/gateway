<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Illuminate\Support\Facades\DB;
use InvalidArgumentException;
use Webong\Gateway\Protocols\SubscriptionState;

final readonly class ManageSubscriptions
{
    public function __construct(
        private SubscriptionRegistry $subscriptions,
        private EndpointHttpTransportResolver $transportResolver,
        private SubscriptionTargetExtensionRegistry $targetExtensions,
    ) {}

    /** @return list<SubscriptionResource> */
    public function route(
        string $endpointKey,
        string $routingScope,
        string $routingKey,
        ?string $channel = null,
        bool $includeRemoved = false,
    ): array {
        return $this->subscriptions->route($endpointKey, $routingScope, $routingKey, $channel, $includeRemoved);
    }

    public function get(string $endpointKey, string $destinationId, ?string $channel = null): SubscriptionResource
    {
        return $this->subscriptions->get($endpointKey, $destinationId, $channel);
    }

    /** @param array<string, mixed>|null $metadata @param array<string, mixed> $targetChanges */
    public function update(
        string $endpointKey,
        string $destinationId,
        string $expectedRevision,
        ?string $url = null,
        ?array $metadata = null,
        ?string $type = null,
        ?string $profile = null,
        bool $profileProvided = false,
        ?string $channel = null,
        array $targetChanges = [],
    ): SubscriptionResource {
        if ($url === null && $metadata === null && $type === null && ! $profileProvided && $targetChanges === []) {
            throw new InvalidArgumentException('Update at least one subscription property.');
        }

        if ($url !== null) {
            $parsed = parse_url($url);
            if (! is_array($parsed)
                || ! in_array($parsed['scheme'] ?? null, ['http', 'https'], true)
                || ($parsed['host'] ?? '') === '') {
                throw new InvalidArgumentException('Subscription URL must be an HTTP(S) URL.');
            }
        }
        if ($type !== null && ! in_array($type, ['relay', 'reply'], true)) {
            throw new InvalidArgumentException("Unsupported subscription type [{$type}].");
        }
        if ($profileProvided && $profile !== null && (trim($profile) === '' || strlen($profile) > 64)) {
            throw new InvalidArgumentException('Subscription profile must be a non-empty string of at most 64 characters.');
        }
        $this->validateMetadata($metadata);

        return $this->subscriptions->mutate(
            $endpointKey,
            $destinationId,
            $expectedRevision,
            function (ResolvedSubscription $resolved) use ($url, $metadata, $type, $profile, $profileProvided, $targetChanges): array {
                $extension = $this->targetExtensions->forDestination($resolved->destination);
                $extensionChanges = [
                    ...$targetChanges,
                    ...($url === null ? [] : ['url' => $url]),
                    ...($profileProvided ? ['profile' => $profile] : []),
                    ...($type === null ? [] : ['type' => $type]),
                ];
                $newTarget = null;
                if ($extension !== null) {
                    $extension->assertMutationAllowed($resolved, $extensionChanges);
                    $extension->validateMetadata($metadata ?? []);
                    $newTarget = $extension->updateTarget($resolved, $targetChanges);
                } elseif ($targetChanges !== []) {
                    throw new InvalidArgumentException('The subscription target does not support these properties.');
                }

                $attributes = [];
                $updatedMetadata = $resolved->destination->metadata;
                if ($profileProvided) {
                    if ($extension !== null) {
                        throw new InvalidArgumentException('The subscription target does not support an HTTP profile.');
                    }
                    if ($profile === null) {
                        unset($updatedMetadata[SubscriptionDefinition::PROFILE_METADATA_KEY]);
                    } else {
                        $this->transportResolver->resolve($resolved->endpoint, $profile);
                        $updatedMetadata[SubscriptionDefinition::PROFILE_METADATA_KEY] = $profile;
                    }
                }
                if ($metadata !== null) {
                    foreach ($metadata as $key => $value) {
                        if (! is_string($key)
                            || str_starts_with($key, '_')
                            || in_array($key, ['subscriber_id', 'delivery_mode', 'profile', 'target_kind'], true)) {
                            throw new InvalidArgumentException("Subscription metadata key [{$key}] is reserved.");
                        }
                        if ($value === null) {
                            unset($updatedMetadata[$key]);
                        } else {
                            $updatedMetadata[$key] = $value;
                        }
                    }
                }
                if ($type !== null) {
                    if ($type === 'reply' && SubscriptionState::status($updatedMetadata) === SubscriptionState::ACTIVE) {
                        $this->subscriptions->assertReplyAvailable($resolved);
                    }
                    $updatedMetadata['delivery_mode'] = $type;
                }
                if ($url !== null) {
                    if ($extension !== null) {
                        throw new InvalidArgumentException('The subscription target does not support an HTTP URL.');
                    }
                    $attributes['target'] = $url;
                } elseif ($newTarget !== null) {
                    $attributes['target'] = $newTarget;
                }
                $attributes['metadata'] = $updatedMetadata;

                return $attributes;
            },
            $channel,
        );
    }

    public function setStatus(
        string $endpointKey,
        string $destinationId,
        string $expectedRevision,
        string $status,
        ?string $channel = null,
    ): SubscriptionResource {
        if (! in_array($status, [SubscriptionState::ACTIVE, SubscriptionState::PAUSED], true)) {
            throw new InvalidArgumentException("Unsupported subscription status [{$status}].");
        }

        return $this->subscriptions->mutate(
            $endpointKey,
            $destinationId,
            $expectedRevision,
            function (ResolvedSubscription $resolved) use ($status): array {
                if ($status === SubscriptionState::ACTIVE
                    && ($resolved->destination->metadata['delivery_mode'] ?? 'relay') === 'reply') {
                    $this->subscriptions->assertReplyAvailable($resolved);
                }

                return ['metadata' => [
                    ...$resolved->destination->metadata,
                    SubscriptionState::STATUS_METADATA_KEY => $status,
                ]];
            },
            $channel,
        );
    }

    public function remove(
        string $endpointKey,
        string $destinationId,
        string $expectedRevision,
        ?string $channel = null,
    ): SubscriptionResource {
        return DB::transaction(fn (): SubscriptionResource => $this->subscriptions->mutate(
            $endpointKey,
            $destinationId,
            $expectedRevision,
            function (ResolvedSubscription $resolved): array {
                $this->targetExtensions->forDestination($resolved->destination)?->remove($resolved);

                return ['metadata' => [
                    ...$resolved->destination->metadata,
                    SubscriptionState::STATUS_METADATA_KEY => SubscriptionState::REMOVED,
                ]];
            },
            $channel,
        ));
    }

    /** @param array<string, mixed>|null $metadata */
    private function validateMetadata(?array $metadata): void
    {
        if ($metadata === null) {
            return;
        }
        foreach (array_keys($metadata) as $key) {
            if (! is_string($key)
                || str_starts_with($key, '_')
                || in_array($key, ['subscriber_id', 'delivery_mode', 'profile', 'target_kind'], true)) {
                throw new InvalidArgumentException("Subscription metadata key [{$key}] is reserved.");
            }
        }
    }
}
