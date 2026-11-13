<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel;

use InvalidArgumentException;
use Webong\Gateway\Protocols\MatchRules;
use Webong\Gateway\Protocols\SubscriptionState;
use Webong\Proxy\DestinationDefinition;
use Webong\Proxy\Enums\WebhookProxyTargetType;

/** Protocol-neutral definition of a Planner subscription target. */
final readonly class SubscriptionDefinition
{
    public const string TARGET_KIND_METADATA_KEY = '_gateway_target_kind';
    public const string TARGET_OPTIONS_METADATA_KEY = '_gateway_target_options';

    /** @param array<string, mixed> $metadata @param array<string, mixed> $targetOptions */
    public function __construct(
        public string $subscriberId,
        public string $subscriptionId,
        public string $group,
        public string $routingScope,
        public string $routingKey,
        public string $targetKind,
        public string $target,
        public MatchRules $match,
        public array $metadata = [],
        public array $targetOptions = [],
        public ?string $channel = null,
        public string $type = 'relay',
    ) {
        $this->assertValid();
    }

    public function toDestinationDefinition(): DestinationDefinition
    {
        $metadata = [
            ...$this->metadata,
            'subscriber_id' => $this->subscriberId,
            'delivery_mode' => $this->type,
            self::TARGET_KIND_METADATA_KEY => $this->targetKind,
            self::TARGET_OPTIONS_METADATA_KEY => $this->targetOptions,
            MatchRules::METADATA_KEY => $this->match->toArray(),
            SubscriptionState::STATUS_METADATA_KEY => SubscriptionState::ACTIVE,
            SubscriptionState::REVISION_METADATA_KEY => SubscriptionState::newRevision(),
        ];

        return new DestinationDefinition(
            ownerId: $this->subscriberId,
            registrationId: $this->subscriptionId,
            webhookGroup: $this->group,
            routingScope: $this->routingScope,
            routingKey: $this->routingKey,
            target: $this->target,
            targetType: WebhookProxyTargetType::REQUEST,
            metadata: $metadata,
            allowsMultipleSubscribers: true,
        );
    }

    private function assertValid(): void
    {
        foreach ([
            'subscriber ID' => $this->subscriberId,
            'subscription ID' => $this->subscriptionId,
            'group' => $this->group,
            'routing scope' => $this->routingScope,
            'routing key' => $this->routingKey,
            'target kind' => $this->targetKind,
            'target' => $this->target,
        ] as $name => $value) {
            if (trim($value) === '') {
                throw new InvalidArgumentException("Subscription {$name} is required.");
            }
        }

        if (preg_match('/\\A[a-z][a-z0-9.-]{0,63}\\z/D', $this->targetKind) !== 1) {
            throw new InvalidArgumentException('Subscription target kind is invalid.');
        }
        if (! in_array($this->type, ['relay', 'reply'], true)) {
            throw new InvalidArgumentException("Unsupported subscription type [{$this->type}].");
        }
        foreach (array_keys($this->metadata) as $key) {
            if (! is_string($key) || str_starts_with($key, '_')
                || in_array($key, ['subscriber_id', 'delivery_mode', 'target_kind'], true)) {
                throw new InvalidArgumentException("Subscription metadata key [{$key}] is reserved.");
            }
        }
    }
}
