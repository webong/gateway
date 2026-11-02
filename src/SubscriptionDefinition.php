<?php

declare(strict_types=1);

namespace Webong\Gateway;

use InvalidArgumentException;
use Webong\Gateway\Protocols\MatchRules;
use Webong\Gateway\Protocols\SubscriptionState;
use Webong\Proxy\DestinationDefinition;
use Webong\Proxy\Enums\WebhookProxyTargetType;

/**
 * PHP-facing subscription DSL. It compiles to the same destination metadata
 * written by the remote HTTP JSON endpoint.
 */
final readonly class SubscriptionDefinition
{
    public const string PROFILE_METADATA_KEY = '_gateway_profile';
    public const string TARGET_KIND_METADATA_KEY = '_gateway_target_kind';

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
        public ?string $profile = null,
        public string $targetKind = 'http',
        public ?string $target = null,
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
        ?string $profile = null,
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
            profile: $profile,
        );
    }

    public function toDestinationDefinition(): DestinationDefinition
    {
        $target = $this->targetKind === 'http' ? $this->url : (string) $this->target;
        $metadata = [
            ...$this->metadata,
            'subscriber_id' => $this->subscriberId,
            'delivery_mode' => $this->type,
            ...($this->profile === null ? [] : [self::PROFILE_METADATA_KEY => $this->profile]),
            ...($this->targetKind === 'http' ? [] : [self::TARGET_KIND_METADATA_KEY => $this->targetKind]),
            MatchRules::METADATA_KEY => $this->match->toArray(),
            SubscriptionState::STATUS_METADATA_KEY => SubscriptionState::ACTIVE,
            SubscriptionState::REVISION_METADATA_KEY => SubscriptionState::newRevision(),
        ];

        return new DestinationDefinition(
            ownerId: $this->subscriberId,
            registrationId: $this->subscriptionId,
            webhookGroup: $this->webhookGroup,
            routingScope: $this->routingScope,
            routingKey: $this->routingKey,
            target: $target,
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
            'webhook group' => $this->webhookGroup,
            'routing scope' => $this->routingScope,
            'routing key' => $this->routingKey,
        ] as $name => $value) {
            if (trim($value) === '') {
                throw new InvalidArgumentException("Subscription {$name} is required.");
            }
        }

        if (preg_match('/\\A[a-z][a-z0-9.-]{0,63}\\z/D', $this->targetKind) !== 1) {
            throw new InvalidArgumentException('Subscription target kind is invalid.');
        }

        if ($this->targetKind === 'http') {
            $parsed = parse_url($this->url);
            if ($this->target !== null || ! is_array($parsed)
                || ! in_array($parsed['scheme'] ?? null, ['http', 'https'], true)
                || ($parsed['host'] ?? '') === '') {
                throw new InvalidArgumentException('Subscription URL must be an HTTP(S) URL.');
            }
        } elseif ($this->target === null || $this->target === '' || $this->url !== '' || $this->profile !== null) {
            throw new InvalidArgumentException('A non-HTTP subscription requires its extension target and cannot use an HTTP URL or profile.');
        }

        foreach (array_keys($this->metadata) as $key) {
            if (! is_string($key)
                || str_starts_with($key, '_')
                || in_array($key, ['subscriber_id', 'delivery_mode', 'profile', 'target_kind'], true)) {
                throw new InvalidArgumentException("Subscription metadata key [{$key}] is reserved.");
            }
        }

        if (! in_array($this->type, ['relay', 'reply'], true)) {
            throw new InvalidArgumentException("Unsupported subscription type [{$this->type}].");
        }

        if ($this->profile !== null && (trim($this->profile) === '' || strlen($this->profile) > 64)) {
            throw new InvalidArgumentException('Subscription profile must be a non-empty string of at most 64 characters.');
        }
    }
}
