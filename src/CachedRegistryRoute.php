<?php

declare(strict_types=1);

namespace Webong\NetGateway;

use InvalidArgumentException;
use Webong\WebProxy\DestinationRecord;
use Webong\WebProxy\Enums\WebhookProxyTargetType;

/** @internal Portable registry data safe to place in a shared Laravel cache. */
final readonly class CachedRegistryRoute
{
    /** @param list<DestinationRecord> $destinations */
    private function __construct(
        public bool $found,
        public ?string $registry,
        public ?string $endpointId,
        public ?string $endpointKey,
        public array $destinations,
        public bool $cacheable,
    ) {
    }

    /** @param list<DestinationRecord> $destinations */
    public static function found(
        string $registry,
        string $endpointId,
        string $endpointKey,
        array $destinations,
        bool $cacheable = true,
    ): self {
        return new self(true, $registry, $endpointId, $endpointKey, $destinations, $cacheable);
    }

    public static function missing(): self
    {
        return new self(false, null, null, null, [], true);
    }

    /** @return array<string, mixed> */
    public function toArray(): array
    {
        return [
            'version' => 1,
            'found' => $this->found,
            'registry' => $this->registry,
            'endpoint_id' => $this->endpointId,
            'endpoint_key' => $this->endpointKey,
            'destinations' => array_map(
                static fn (DestinationRecord $destination): array => [
                    'id' => $destination->id,
                    'endpoint_id' => $destination->endpoint_id,
                    'owner_id' => $destination->owner_id,
                    'registration_id' => $destination->registration_id,
                    'webhook_group' => $destination->webhook_group,
                    'routing_scope' => $destination->routing_scope,
                    'routing_key' => $destination->routing_key,
                    'target_type' => $destination->target_type->value,
                    'target' => $destination->target,
                    'metadata' => $destination->metadata,
                    'is_active' => $destination->is_active,
                ],
                $this->destinations,
            ),
        ];
    }

    /** @param array<string, mixed> $value */
    public static function fromArray(array $value): self
    {
        if (($value['version'] ?? null) !== 1 || ! is_bool($value['found'] ?? null)) {
            throw new InvalidArgumentException('Cached registry route is malformed.');
        }

        if ($value['found'] === false) {
            return self::missing();
        }

        foreach (['registry', 'endpoint_id', 'endpoint_key'] as $key) {
            if (! is_string($value[$key] ?? null) || $value[$key] === '') {
                throw new InvalidArgumentException('Cached registry route is malformed.');
            }
        }

        if (! is_array($value['destinations'] ?? null)) {
            throw new InvalidArgumentException('Cached registry route destinations are malformed.');
        }

        $destinations = array_map(static function (mixed $destination): DestinationRecord {
            if (! is_array($destination)) {
                throw new InvalidArgumentException('Cached registry destination is malformed.');
            }

            $targetType = is_string($destination['target_type'] ?? null)
                ? WebhookProxyTargetType::tryFrom($destination['target_type'])
                : null;

            if ($targetType === null || ! is_array($destination['metadata'] ?? null)) {
                throw new InvalidArgumentException('Cached registry destination is malformed.');
            }

            foreach ([
                'id',
                'endpoint_id',
                'owner_id',
                'registration_id',
                'webhook_group',
                'routing_scope',
                'routing_key',
                'target',
            ] as $key) {
                if (! is_string($destination[$key] ?? null)) {
                    throw new InvalidArgumentException('Cached registry destination is malformed.');
                }
            }

            if (! is_bool($destination['is_active'] ?? null)) {
                throw new InvalidArgumentException('Cached registry destination is malformed.');
            }

            return new DestinationRecord(
                id: $destination['id'],
                endpoint_id: $destination['endpoint_id'],
                owner_id: $destination['owner_id'],
                registration_id: $destination['registration_id'],
                webhook_group: $destination['webhook_group'],
                routing_scope: $destination['routing_scope'],
                routing_key: $destination['routing_key'],
                target_type: $targetType,
                target: $destination['target'],
                metadata: $destination['metadata'],
                is_active: $destination['is_active'],
            );
        }, array_values($value['destinations']));

        return self::found(
            registry: $value['registry'],
            endpointId: $value['endpoint_id'],
            endpointKey: $value['endpoint_key'],
            destinations: $destinations,
        );
    }
}
