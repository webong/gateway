<?php

declare(strict_types=1);

namespace Webong\Gateway\Protocol;

use InvalidArgumentException;

final readonly class IngressRequest
{
    /** @param array<string, list<string>> $headers @param array<string, string> $attributes */
    public function __construct(
        public string $deliveryId,
        public string $method,
        public string $host,
        public string $path,
        public string $rawQuery,
        public array $headers,
        public string $body,
        public string $scheme = 'https',
        public string $protocol = 'http',
        public string $event = 'request',
        public string $sessionId = '',
        public array $attributes = [],
    ) {
    }

    /** @param array<string, mixed> $payload */
    public static function fromArray(array $payload): self
    {
        $deliveryId = self::string($payload, 'delivery_id');
        $method = strtoupper(self::string($payload, 'method'));
        $path = self::string($payload, 'path');
        $body = base64_decode(self::string($payload, 'body', ''), true);

        if ($deliveryId === '' || $method === '' || $path === '' || $body === false) {
            throw new InvalidArgumentException('Invalid Go ingress request.');
        }

        $rawHeaders = $payload['headers'] ?? [];
        if (! is_array($rawHeaders)) {
            throw new InvalidArgumentException('Ingress headers must be a string-to-list map.');
        }

        $headers = [];
        foreach ($rawHeaders as $name => $values) {
            if (! is_string($name) || ! is_array($values)) {
                throw new InvalidArgumentException('Ingress headers must be a string-to-list map.');
            }

            $headers[$name] = array_values(array_map(
                static fn (mixed $value): string => (string) $value,
                $values,
            ));
        }

        return new self(
            deliveryId: $deliveryId,
            method: $method,
            scheme: self::string($payload, 'scheme', 'https'),
            host: self::string($payload, 'host', ''),
            path: $path,
            rawQuery: self::string($payload, 'raw_query', ''),
            headers: $headers,
            body: $body,
            protocol: self::string($payload, 'protocol', 'http'),
            event: self::string($payload, 'event', 'request'),
            sessionId: self::string($payload, 'session_id', ''),
            attributes: self::attributes($payload['attributes'] ?? []),
        );
    }

    /** @return array<string, string> */
    private static function attributes(mixed $attributes): array
    {
        if (! is_array($attributes)) {
            throw new InvalidArgumentException('Ingress attributes must be a string map.');
        }

        $normalized = [];
        foreach ($attributes as $name => $value) {
            if (! is_string($name) || ! is_scalar($value)) {
                throw new InvalidArgumentException('Ingress attributes must be a string map.');
            }

            $normalized[$name] = (string) $value;
        }

        return $normalized;
    }

    /** @param array<string, mixed> $payload */
    private static function string(array $payload, string $key, string $default = ''): string
    {
        $value = $payload[$key] ?? $default;

        return is_string($value) ? $value : (string) $value;
    }
}
