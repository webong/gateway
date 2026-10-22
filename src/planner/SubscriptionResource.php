<?php

declare(strict_types=1);

namespace Webong\Gateway;

use InvalidArgumentException;
use Webong\Proxy\DestinationRecord;
use Webong\Gateway\Protocol\MatchRules;
use Webong\Gateway\Protocol\SubscriptionState;

final readonly class SubscriptionResource
{
    public function __construct(
        public string $endpointKey,
        public DestinationRecord $destination,
    ) {
    }

    public function revision(): string
    {
        return SubscriptionState::revision($this->destination->metadata);
    }

    public function etag(): string
    {
        return SubscriptionState::etag($this->revision());
    }

    /** @return array<string, mixed> */
    public function toArray(): array
    {
        $storedMatch = $this->destination->metadata[MatchRules::METADATA_KEY] ?? null;
        if ($storedMatch === null) {
            $match = MatchRules::any();
        } elseif (is_array($storedMatch)) {
            $match = MatchRules::fromArray($storedMatch);
        } else {
            throw new InvalidArgumentException('The subscription contains malformed persisted match rules.');
        }

        $shell = ShellSubscriptionTarget::isShell($this->destination->target);

        return [
            'id' => $this->destination->id,
            'endpoint_key' => $this->endpointKey,
            'subscriber_id' => $this->subscriberId(),
            'subscription_id' => $this->destination->registration_id,
            'type' => $this->type(),
            'webhook_group' => $this->destination->webhook_group,
            'routing_scope' => $this->destination->routing_scope,
            'routing_key' => $this->destination->routing_key,
            'target_kind' => $shell ? 'shell' : 'http',
            'url' => $shell ? null : $this->destination->target,
            'hook' => $shell ? ShellSubscriptionTarget::hook($this->destination->target) : null,
            'ssh' => ($sshId = ShellSubscriptionTarget::sshId($this->destination->target)) === null
                ? null : app(ShellSshExecutionStore::class)->publicIdentity($sshId),
            'profile' => $shell ? null : $this->profile(),
            'metadata' => $this->publicMetadata(),
            'match' => $match->toArray(),
            'status' => SubscriptionState::status($this->destination->metadata),
            'revision' => $this->revision(),
        ];
    }

    public function type(): string
    {
        $type = $this->destination->metadata['delivery_mode'] ?? 'relay';

        return is_string($type) ? $type : 'relay';
    }

    /** @return array<string, mixed> */
    public function publicMetadata(): array
    {
        return array_filter(
            $this->destination->metadata,
            static fn (mixed $key): bool => is_string($key)
                && ! str_starts_with($key, '_')
                && ! in_array($key, ['subscriber_id', 'delivery_mode', 'profile'], true),
            ARRAY_FILTER_USE_KEY,
        );
    }

    private function subscriberId(): string
    {
        $subscriberId = $this->destination->metadata['subscriber_id'] ?? $this->destination->owner_id;

        return is_scalar($subscriberId) ? (string) $subscriberId : $this->destination->owner_id;
    }

    private function profile(): ?string
    {
        $profile = $this->destination->metadata[SubscriptionDefinition::PROFILE_METADATA_KEY] ?? null;

        return is_string($profile) && $profile !== '' ? $profile : null;
    }
}
