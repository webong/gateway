<?php

declare(strict_types=1);

namespace Webong\WebRelay\Protocol;

use InvalidArgumentException;

final readonly class GatewayDecision
{
    /** @param array<string, list<string>> $headers @param list<GatewayDelivery> $deliveries @param array<string, string> $metadata */
    public function __construct(
        public Protocol $protocol,
        public GatewayAction $action,
        public int $statusCode = 0,
        public array $headers = [],
        public string $message = '',
        public string $payload = '',
        public array $deliveries = [],
        public array $metadata = [],
        public string $version = 'v1',
    ) {
        if ($this->version !== 'v1') {
            throw new InvalidArgumentException('Unsupported gateway decision version.');
        }

        if ($this->action === GatewayAction::DELIVER && $this->deliveries === []) {
            throw new InvalidArgumentException('Deliver decisions require a destination.');
        }

        foreach ($this->deliveries as $delivery) {
            if (! $delivery instanceof GatewayDelivery) {
                throw new InvalidArgumentException('Gateway deliveries must be GatewayDelivery objects.');
            }
        }
    }

    /** @return array<string, mixed> */
    public function toArray(): array
    {
        return array_filter([
            'version' => $this->version,
            'protocol' => $this->protocol->value,
            'action' => $this->action->value,
            'status_code' => $this->statusCode,
            'headers' => $this->headers,
            'message' => $this->message,
            'payload' => base64_encode($this->payload),
            'deliveries' => array_map(
                static fn (GatewayDelivery $delivery): array => $delivery->toArray(),
                $this->deliveries,
            ),
            'metadata' => $this->metadata,
        ], static fn (mixed $value): bool => $value !== '' && $value !== 0 && $value !== []);
    }
}
