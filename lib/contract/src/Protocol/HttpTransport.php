<?php

declare(strict_types=1);

namespace Webong\Gateway\Protocol;

use InvalidArgumentException;

final readonly class HttpTransport
{
    private const int MAX_TIMEOUT_SECONDS = 300;

    /** @param list<string> $noProxy @param array<string, list<string>> $streamHeaders */
    public function __construct(
        public ?string $httpProxy = null,
        public ?string $httpsProxy = null,
        public array $noProxy = [],
        public array $streamHeaders = [],
        public ?float $connectTimeoutSeconds = null,
        public ?float $requestTimeoutSeconds = null,
    ) {
        foreach (['HTTP' => $this->httpProxy, 'HTTPS' => $this->httpsProxy] as $name => $proxy) {
            if ($proxy === null) {
                continue;
            }

            $parsed = parse_url($proxy);
            if (! is_array($parsed)
                || ! in_array($parsed['scheme'] ?? null, ['http', 'https', 'socks5', 'socks5h'], true)
                || ($parsed['host'] ?? '') === '') {
                throw new InvalidArgumentException("{$name} proxy must be an HTTP(S) or SOCKS5 URL.");
            }
        }

        foreach ($this->noProxy as $host) {
            if (! is_string($host) || trim($host) === '') {
                throw new InvalidArgumentException('No-proxy entries must be non-empty strings.');
            }
        }

        foreach ($this->streamHeaders as $name => $values) {
            if (! is_string($name) || preg_match('/^[!#$%&\'*+.^_`|~0-9A-Za-z-]+$/', $name) !== 1 || self::reservedStreamHeader($name) || ! is_array($values)) {
                throw new InvalidArgumentException('Webstream headers must be a valid string-to-list map.');
            }
            foreach ($values as $value) {
                if (! is_string($value) || str_contains($value, "\r") || str_contains($value, "\n")) {
                    throw new InvalidArgumentException('Webstream header values must not contain line breaks.');
                }
            }
        }

        foreach (['connect' => $this->connectTimeoutSeconds, 'request' => $this->requestTimeoutSeconds] as $name => $seconds) {
            if ($seconds !== null && ($seconds <= 0 || $seconds > self::MAX_TIMEOUT_SECONDS)) {
                throw new InvalidArgumentException(ucfirst($name).' timeout must be greater than 0 and at most 300 seconds.');
            }
        }
    }

    private static function reservedStreamHeader(string $name): bool
    {
        return in_array(strtolower($name), ['connection', 'content-length', 'host', 'last-event-id', 'transfer-encoding', 'upgrade'], true);
    }

    /** @param array<string, mixed> $payload */
    public static function fromArray(array $payload): self
    {
        $proxy = $payload['proxy'] ?? [];
        if (! is_array($proxy)) {
            throw new InvalidArgumentException('HTTP transport proxy must be an object.');
        }

        $noProxy = $proxy['no_proxy'] ?? [];
        if (! is_array($noProxy)) {
            throw new InvalidArgumentException('HTTP transport no_proxy must be a list.');
        }
        foreach (['http', 'https'] as $scheme) {
            if (array_key_exists($scheme, $proxy) && ! is_string($proxy[$scheme])) {
                throw new InvalidArgumentException("HTTP transport {$scheme} proxy must be a string.");
            }
        }
        foreach (['connect_timeout_seconds', 'request_timeout_seconds'] as $timeout) {
            if (array_key_exists($timeout, $payload)
                && ! is_int($payload[$timeout])
                && ! is_float($payload[$timeout])) {
                throw new InvalidArgumentException("HTTP transport {$timeout} must be numeric.");
            }
        }

        $headers = $payload['stream_headers'] ?? [];
        if (! is_array($headers)) {
            throw new InvalidArgumentException('Webstream headers must be an object.');
        }
        $normalizedHeaders = [];
        foreach ($headers as $name => $values) {
            if (! is_string($name) || ! is_array($values)) {
                throw new InvalidArgumentException('Webstream headers must be a string-to-list map.');
            }
            $normalizedHeaders[$name] = array_values($values);
        }

        return new self(
            httpProxy: $proxy['http'] ?? null,
            httpsProxy: $proxy['https'] ?? null,
            noProxy: array_values($noProxy),
            streamHeaders: $normalizedHeaders,
            connectTimeoutSeconds: isset($payload['connect_timeout_seconds'])
                ? (float) $payload['connect_timeout_seconds']
                : null,
            requestTimeoutSeconds: isset($payload['request_timeout_seconds'])
                ? (float) $payload['request_timeout_seconds']
                : null,
        );
    }

    /** @return array<string, mixed> */
    public function toArray(): array
    {
        return array_filter([
            'proxy' => array_filter([
                'http' => $this->httpProxy,
                'https' => $this->httpsProxy,
                'no_proxy' => $this->noProxy,
            ], static fn (mixed $value): bool => $value !== null && $value !== []),
            'connect_timeout_seconds' => $this->connectTimeoutSeconds,
            'request_timeout_seconds' => $this->requestTimeoutSeconds,
            'stream_headers' => $this->streamHeaders,
        ], static fn (mixed $value): bool => $value !== null && $value !== []);
    }
}
