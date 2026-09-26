<?php

declare(strict_types=1);

namespace Webong\Gateway\Protocol;

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
        public string $adapter = '',
    ) {
        if (trim($this->target) === '') {
            throw new InvalidArgumentException('Gateway delivery target is required.');
        }

        if ($this->adapter !== '' && preg_match('/^[a-z][a-z0-9._-]{0,63}$/', $this->adapter) !== 1) {
            throw new InvalidArgumentException('Gateway delivery adapter is invalid.');
        }
    }

    /** @return array<string, mixed> */
    public function toArray(): array
    {
        return array_filter([
            'protocol' => $this->protocol->value,
            'adapter' => $this->adapter,
            'target' => $this->target,
            'subscriber_id' => $this->subscriberId,
            'headers' => $this->headers,
            'attributes' => $this->attributes,
            'payload' => base64_encode($this->payload),
        ], static fn (mixed $value): bool => $value !== '' && $value !== []);
    }
}
