<?php

declare(strict_types=1);

namespace Webong\Gateway;

use InvalidArgumentException;
use Webong\WebProxy\DestinationDefinition;
use Webong\WebProxy\Enums\WebhookProxyTargetType;
use Webong\Gateway\Protocol\MatchRules;
use Webong\Gateway\Protocol\SubscriptionState;

/**
 * PHP-facing subscription DSL. It compiles to the same destination metadata
 * written by the remote HTTP JSON endpoint.
 */
final readonly class SubscriptionDefinition
{
    /** @param array<string, mixed> $metadata */
    public function __construct(
        public string $subscriberId,
        public string $subscriptionId,
        public string $webhookGroup,
        public string $routingScope,
        public string $routingKey,
        public string $url,
        public MatchRules $match,
        public array $metadata = [],
        public ?string $channel = null,
        public string $type = 'relay',
    ) {
        $this->assertValid();
    }

    /** @param array<string, string|list<string>> $rules @param array<string, mixed> $metadata */
    public static function matching(
        string $subscriberId,
        string $subscriptionId,
        string $webhookGroup,
        string $routingScope,
        string $routingKey,
        string $url,
        array $rules,
        array $metadata = [],
        ?string $channel = null,
        string $type = 'relay',
    ): self {
        return new self(
            subscriberId: $subscriberId,
            subscriptionId: $subscriptionId,
            webhookGroup: $webhookGroup,
            routingScope: $routingScope,
            routingKey: $routingKey,
            url: $url,
            match: MatchRules::make($rules),
            metadata: $metadata,
            channel: $channel,
            type: $type,
        );
    }

    public function toDestinationDefinition(): DestinationDefinition
    {
        return new DestinationDefinition(
            ownerId: $this->subscriberId,
            registrationId: $this->subscriptionId,
            webhookGroup: $this->webhookGroup,
            routingScope: $this->routingScope,
            routingKey: $this->routingKey,
            target: $this->url,
            targetType: WebhookProxyTargetType::REQUEST,
            metadata: [
                ...$this->metadata,
                'subscriber_id' => $this->subscriberId,
                'delivery_mode' => $this->type,
                MatchRules::METADATA_KEY => $this->match->toArray(),
                SubscriptionState::STATUS_METADATA_KEY => SubscriptionState::ACTIVE,
                SubscriptionState::REVISION_METADATA_KEY => SubscriptionState::newRevision(),
            ],
            allowsMultipleSubscribers: true,
        );
    }

    private function assertValid(): void
    {
        foreach ([
            'subscriber ID' => $this->subscriberId,
            'subscription ID' => $this->subscriptionId,
            'webhook group' => $this->webhookGroup,
            'routing scope' => $this->routingScope,
            'routing key' => $this->routingKey,
        ] as $name => $value) {
            if (trim($value) === '') {
                throw new InvalidArgumentException("Subscription {$name} is required.");
            }
        }

        $parsed = parse_url($this->url);
        if (! is_array($parsed)
            || ! in_array($parsed['scheme'] ?? null, ['http', 'https'], true)
            || ($parsed['host'] ?? '') === '') {
            throw new InvalidArgumentException('Subscription URL must be an HTTP(S) URL.');
        }

        foreach (array_keys($this->metadata) as $key) {
            if (! is_string($key)
                || str_starts_with($key, '_')
                || in_array($key, ['subscriber_id', 'delivery_mode'], true)) {
                throw new InvalidArgumentException("Subscription metadata key [{$key}] is reserved.");
            }
        }

        if (! in_array($this->type, ['relay', 'reply'], true)) {
            throw new InvalidArgumentException("Unsupported subscription type [{$this->type}].");
        }
    }
}
