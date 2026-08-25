<?php

declare(strict_types=1);

namespace Webong\WebRelay;

use InvalidArgumentException;
use Webong\WebRelay\Protocol\SubscriptionState;

final readonly class ManageSubscriptions
{
    public function __construct(private SubscriptionRegistry $subscriptions)
    {
    }

    /** @return list<SubscriptionResource> */
    public function route(
        string $endpointKey,
        string $routingScope,
        string $routingKey,
        ?string $channel = null,
        bool $includeRemoved = false,
    ): array {
        return $this->subscriptions->route(
            $endpointKey,
            $routingScope,
            $routingKey,
            $channel,
            $includeRemoved,
        );
    }

    public function get(string $endpointKey, string $destinationId, ?string $channel = null): SubscriptionResource
    {
        return $this->subscriptions->get($endpointKey, $destinationId, $channel);
    }

    /** @param array<string, mixed>|null $metadata */
    public function update(
        string $endpointKey,
        string $destinationId,
        string $expectedRevision,
        ?string $url = null,
        ?array $metadata = null,
        ?string $type = null,
        ?string $channel = null,
    ): SubscriptionResource {
        if ($url === null && $metadata === null && $type === null) {
            throw new InvalidArgumentException('Update at least one of url, metadata, or type.');
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

        if ($metadata !== null) {
            foreach (array_keys($metadata) as $key) {
                if (! is_string($key)
                    || str_starts_with($key, '_')
                    || in_array($key, ['subscriber_id', 'delivery_mode'], true)) {
                    throw new InvalidArgumentException("Subscription metadata key [{$key}] is reserved.");
                }
            }
        }

        return $this->subscriptions->mutate(
            $endpointKey,
            $destinationId,
            $expectedRevision,
            function (ResolvedSubscription $resolved) use ($url, $metadata, $type): array {
                $attributes = [];
                $updatedMetadata = $resolved->destination->metadata;

                if ($metadata !== null) {
                    foreach ($metadata as $key => $value) {
                        if ($value === null) {
                            unset($updatedMetadata[$key]);
                        } else {
                            $updatedMetadata[$key] = $value;
                        }
                    }
                }

                if ($type !== null) {
                    if ($type === 'reply'
                        && SubscriptionState::status($updatedMetadata) === SubscriptionState::ACTIVE) {
                        $this->subscriptions->assertReplyAvailable($resolved);
                    }
                    $updatedMetadata['delivery_mode'] = $type;
                }

                if ($url !== null) {
                    $attributes['target'] = $url;
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
        return $this->subscriptions->mutate(
            $endpointKey,
            $destinationId,
            $expectedRevision,
            static fn (ResolvedSubscription $resolved): array => ['metadata' => [
                ...$resolved->destination->metadata,
                SubscriptionState::STATUS_METADATA_KEY => SubscriptionState::REMOVED,
            ]],
            $channel,
        );
    }
}
