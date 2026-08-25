<?php

declare(strict_types=1);

namespace Webong\WebRelay\Protocol;

use InvalidArgumentException;

final readonly class GatewayEvent
{
    /** @param array<string, list<string>> $headers @param array<string, string> $attributes */
    public function __construct(
        public string $id,
        public Protocol $protocol,
        public EventKind $kind,
        public string $route,
        public string $sessionId = '',
        public string $host = '',
        public string $method = '',
        public string $rawQuery = '',
        public array $headers = [],
        public array $attributes = [],
        public string $payload = '',
    ) {
        if ($this->id === '' || $this->route === '') {
            throw new InvalidArgumentException('Gateway event id and route are required.');
        }

        if ($this->protocol === Protocol::HTTP && $this->kind !== EventKind::REQUEST) {
            throw new InvalidArgumentException('HTTP gateway events must use the request kind.');
        }
    }

    /** @param array<string, mixed> $payload */
    public static function fromArray(array $payload): self
    {
        $body = base64_decode((string) ($payload['payload'] ?? ''), true);
        $protocol = Protocol::tryFrom((string) ($payload['protocol'] ?? ''));
        $kind = EventKind::tryFrom((string) ($payload['kind'] ?? ''));

        if ($body === false || $protocol === null || $kind === null) {
            throw new InvalidArgumentException('Invalid gateway event.');
        }

        return new self(
            id: (string) ($payload['id'] ?? ''),
            protocol: $protocol,
            kind: $kind,
            sessionId: (string) ($payload['session_id'] ?? ''),
            host: (string) ($payload['host'] ?? ''),
            route: (string) ($payload['route'] ?? ''),
            method: strtoupper((string) ($payload['method'] ?? '')),
            rawQuery: (string) ($payload['raw_query'] ?? ''),
            headers: self::headers($payload['headers'] ?? []),
            attributes: self::attributes($payload['attributes'] ?? []),
            payload: $body,
        );
    }

    /** @return array<string, mixed> */
    public function toArray(): array
    {
        return array_filter([
            'id' => $this->id,
            'protocol' => $this->protocol->value,
            'kind' => $this->kind->value,
            'session_id' => $this->sessionId,
            'host' => $this->host,
            'route' => $this->route,
            'method' => $this->method,
            'raw_query' => $this->rawQuery,
            'headers' => $this->headers,
            'attributes' => $this->attributes,
            'payload' => base64_encode($this->payload),
        ], static fn (mixed $value): bool => $value !== '' && $value !== []);
    }

    /** @return array<string, list<string>> */
    private static function headers(mixed $headers): array
    {
        if (! is_array($headers)) {
            throw new InvalidArgumentException('Gateway event headers must be a string-to-list map.');
        }

        $normalized = [];
        foreach ($headers as $name => $values) {
            if (! is_string($name) || ! is_array($values)) {
                throw new InvalidArgumentException('Gateway event headers must be a string-to-list map.');
            }

            $normalized[$name] = array_values(array_map(
                static fn (mixed $value): string => (string) $value,
                $values,
            ));
        }

        return $normalized;
    }

    /** @return array<string, string> */
    private static function attributes(mixed $attributes): array
    {
        if (! is_array($attributes)) {
            throw new InvalidArgumentException('Gateway event attributes must be a string map.');
        }

        $normalized = [];
        foreach ($attributes as $name => $value) {
            if (! is_string($name) || ! is_scalar($value)) {
                throw new InvalidArgumentException('Gateway event attributes must be a string map.');
            }

            $normalized[$name] = (string) $value;
        }

        return $normalized;
    }
}
