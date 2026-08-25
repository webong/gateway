<?php

declare(strict_types=1);

namespace Webong\NetGateway\Protocol;

use InvalidArgumentException;

final readonly class Delivery
{
    /** @param array<string, list<string>> $headers */
    public function __construct(
        public string $url,
        public string $subscriberId = '',
        public string $id = '',
        public string $method = '',
        public string $rawQuery = '',
        public array $headers = [],
        public string $body = '',
    ) {
        $this->assertValid();
    }

    /** @param array<string, mixed> $payload */
    public static function fromArray(array $payload): self
    {
        $body = base64_decode((string) ($payload['body'] ?? ''), true);
        if ($body === false || trim((string) ($payload['url'] ?? '')) === '') {
            throw new InvalidArgumentException('Invalid relay delivery.');
        }

        $rawHeaders = $payload['headers'] ?? [];
        if (! is_array($rawHeaders)) {
            throw new InvalidArgumentException('Delivery headers must be a string-to-list map.');
        }

        $headers = [];
        foreach ($rawHeaders as $name => $values) {
            if (! is_string($name) || ! is_array($values)) {
                throw new InvalidArgumentException('Delivery headers must be a string-to-list map.');
            }

            $headers[$name] = array_values(array_map(
                static fn (mixed $value): string => (string) $value,
                $values,
            ));
        }

        return new self(
            url: trim((string) $payload['url']),
            subscriberId: (string) ($payload['subscriber_id'] ?? ''),
            id: (string) ($payload['id'] ?? ''),
            method: strtoupper((string) ($payload['method'] ?? '')),
            rawQuery: (string) ($payload['raw_query'] ?? ''),
            headers: $headers,
            body: $body,
        );
    }

    /** @return array<string, mixed> */
    public function toArray(): array
    {
        return array_filter([
            'id' => $this->id,
            'subscriber_id' => $this->subscriberId,
            'url' => $this->url,
            'method' => $this->method,
            'raw_query' => $this->rawQuery,
            'headers' => $this->headers,
            'body' => base64_encode($this->body),
        ], static fn (mixed $value): bool => $value !== '' && $value !== []);
    }

    public function assertValid(): void
    {
        if (trim($this->url) === '') {
            throw new InvalidArgumentException('Relay delivery URL is required.');
        }

        $parsed = parse_url($this->url);
        if (! is_array($parsed) || ! in_array($parsed['scheme'] ?? null, ['http', 'https'], true) || ($parsed['host'] ?? '') === '') {
            throw new InvalidArgumentException('Relay delivery URL must be an HTTP(S) URL.');
        }

        if ($this->method !== '' && ! in_array($this->method, [
            'CONNECT', 'DELETE', 'GET', 'HEAD', 'OPTIONS', 'PATCH', 'POST', 'PUT', 'TRACE',
        ], true)) {
            throw new InvalidArgumentException("Unsupported relay delivery method [{$this->method}].");
        }
    }
}
