<?php

declare(strict_types=1);

namespace Webong\Gateway\Protocols;

use DateTimeImmutable;
use InvalidArgumentException;

final readonly class GatewayObservation
{
    /** @param array<string, string> $attributes */
    public function __construct(
        public string $id,
        public string $type,
        public DateTimeImmutable $occurredAt,
        public array $attributes = [],
        public string $version = 'v1',
    ) {
        if ($this->version !== 'v1') {
            throw new InvalidArgumentException('Unsupported Gateway observation version.');
        }
        if ($this->id === '' || strlen($this->id) > 200) {
            throw new InvalidArgumentException('Gateway observation id is required and must not exceed 200 bytes.');
        }
        if (preg_match('/^[a-z][a-z0-9.-]{0,63}$/D', $this->type) !== 1) {
            throw new InvalidArgumentException('Invalid Gateway observation type.');
        }
        if (count($this->attributes) > 64) {
            throw new InvalidArgumentException('Gateway observation attributes exceed 64 entries.');
        }
        foreach ($this->attributes as $key => $value) {
            if (! is_string($key) || ! is_string($value) || trim($key) === '' || strlen($key) > 64 || strlen($value) > 4096) {
                throw new InvalidArgumentException("Invalid Gateway observation attribute [{$key}].");
            }
        }
    }

    /** @param array<string, mixed> $payload */
    public static function fromArray(array $payload): self
    {
        if (array_diff(array_keys($payload), ['version', 'id', 'type', 'occurred_at', 'attributes']) !== []
            || ! is_array($payload['attributes'] ?? [])) {
            throw new InvalidArgumentException('Invalid Gateway observation.');
        }
        $occurred = (string) ($payload['occurred_at'] ?? '');
        if (preg_match('/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/D', $occurred) !== 1) {
            throw new InvalidArgumentException('Invalid Gateway observation occurrence time.');
        }
        try {
            $occurredAt = new DateTimeImmutable($occurred);
        } catch (\Throwable) {
            throw new InvalidArgumentException('Invalid Gateway observation occurrence time.');
        }

        return new self(
            id: (string) ($payload['id'] ?? ''),
            type: (string) ($payload['type'] ?? ''),
            occurredAt: $occurredAt,
            attributes: $payload['attributes'],
            version: (string) ($payload['version'] ?? ''),
        );
    }
}
