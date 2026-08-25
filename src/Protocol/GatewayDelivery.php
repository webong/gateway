<?php

declare(strict_types=1);

namespace Webong\WebRelay\Protocol;

use InvalidArgumentException;

final readonly class GatewayDelivery
{
    /** @param array<string, list<string>> $headers @param array<string, string> $attributes */
    public function __construct(
        public Protocol $protocol,
        public string $target,
        public string $subscriberId = '',
        public array $headers = [],
        public array $attributes = [],
        public string $payload = '',
    ) {
        if (trim($this->target) === '') {
            throw new InvalidArgumentException('Gateway delivery target is required.');
        }
    }

    /** @return array<string, mixed> */
    public function toArray(): array
    {
        return array_filter([
            'protocol' => $this->protocol->value,
            'target' => $this->target,
            'subscriber_id' => $this->subscriberId,
            'headers' => $this->headers,
            'attributes' => $this->attributes,
            'payload' => base64_encode($this->payload),
        ], static fn (mixed $value): bool => $value !== '' && $value !== []);
    }
}
