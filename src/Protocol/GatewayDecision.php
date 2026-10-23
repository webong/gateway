<?php

declare(strict_types=1);

namespace Webong\Gateway\Protocols;

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
        public ?GatewayDelivery $reply = null,
        public array $deliveries = [],
        public array $metadata = [],
        public string $version = 'v1',
        public ?GatewayDelivery $stream = null,
    ) {
        if ($this->version !== 'v1') {
            throw new InvalidArgumentException('Unsupported gateway decision version.');
        }

        if ($this->action === GatewayAction::DELIVER && $this->reply === null && $this->deliveries === []) {
            throw new InvalidArgumentException('Deliver decisions require a reply or destination.');
        }
        if ($this->action === GatewayAction::SUBMIT
            && ($this->protocol !== Protocol::SMTP || ! array_is_list($this->deliveries) || count($this->deliveries) !== 1
                || ! $this->deliveries[0] instanceof GatewayDelivery
                || $this->deliveries[0]->protocol !== Protocol::SMTP || $this->reply !== null)) {
            throw new InvalidArgumentException('SMTP submit decisions require one SMTP delivery.');
        }
        if ($this->action === GatewayAction::QUARANTINE
            && ($this->protocol !== Protocol::SMTP || $this->deliveries !== [] || $this->reply !== null || $this->stream !== null)) {
            throw new InvalidArgumentException('SMTP quarantine decisions cannot include deliveries.');
        }
        if ($this->action === GatewayAction::STREAM
            && ($this->protocol !== Protocol::WEBSTREAM || $this->stream === null || $this->stream->protocol !== Protocol::HTTP)) {
            throw new InvalidArgumentException('Webstream decisions require one HTTP stream source.');
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
            'reply' => $this->reply?->toArray(),
            'deliveries' => array_map(
                static fn (GatewayDelivery $delivery): array => $delivery->toArray(),
                $this->deliveries,
            ),
            'metadata' => $this->metadata,
            'stream' => $this->stream?->toArray(),
        ], static fn (mixed $value): bool => $value !== null && $value !== '' && $value !== 0 && $value !== []);
    }
}
