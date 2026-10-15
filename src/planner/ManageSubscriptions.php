<?php

declare(strict_types=1);

namespace Webong\Gateway;

use InvalidArgumentException;
use Webong\Gateway\Protocol\SubscriptionState;

final readonly class ManageSubscriptions
{
    public function __construct(
        private SubscriptionRegistry $subscriptions,
        private EndpointHttpTransportResolver $transportResolver,
        private ShellSubscriptionPolicy $shellPolicy,
    ) {}

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
        ?string $profile = null,
        bool $profileProvided = false,
        ?string $channel = null,
        ?string $shellHook = null,
    ): SubscriptionResource {
        if ($url === null && $metadata === null && $type === null && ! $profileProvided && $shellHook === null) {
            throw new InvalidArgumentException('Update at least one of url, hook, metadata, type, or profile.');
        }

        if ($shellHook !== null) {
            ShellSubscriptionTarget::assertHook($shellHook);
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

        if ($metadata !== null) {
            foreach (array_keys($metadata) as $key) {
                if (! is_string($key)
                    || str_starts_with($key, '_')
                    || in_array($key, ['subscriber_id', 'delivery_mode', 'profile'], true)) {
                    throw new InvalidArgumentException("Subscription metadata key [{$key}] is reserved.");
                }
            }
        }

        return $this->subscriptions->mutate(
            $endpointKey,
            $destinationId,
            $expectedRevision,
            function (ResolvedSubscription $resolved) use ($url, $metadata, $type, $profile, $profileProvided, $shellHook): array {
                $attributes = [];
                $updatedMetadata = $resolved->destination->metadata;
                $shell = ShellSubscriptionTarget::isShell($resolved->destination->target);

                if (($shell && ($url !== null || $profileProvided || $type === 'relay'))
                    || (! $shell && $shellHook !== null)) {
                    throw new InvalidArgumentException('A subscription target kind cannot be changed; Shell replies accept only hook and metadata updates.');
                }
                if ($shellHook !== null) {
                    $subscriberId = $updatedMetadata['subscriber_id'] ?? $resolved->destination->owner_id;
                    if (! is_string($subscriberId)) {
                        throw new InvalidArgumentException('The subscription contains an invalid subscriber identity.');
                    }
                    $this->shellPolicy->assertAllowed($resolved->endpoint->record->endpoint_key, $subscriberId, $shellHook);
                }

                if ($profileProvided) {
                    if ($profile === null) {
                        unset($updatedMetadata[SubscriptionDefinition::PROFILE_METADATA_KEY]);
                    } else {
                        $this->transportResolver->resolve($resolved->endpoint, $profile);
                        $updatedMetadata[SubscriptionDefinition::PROFILE_METADATA_KEY] = $profile;
                    }
                }

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
                if ($shellHook !== null) {
                    $attributes['target'] = ShellSubscriptionTarget::encode($shellHook);
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
