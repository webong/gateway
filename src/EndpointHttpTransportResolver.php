<?php

declare(strict_types=1);

namespace Webong\Gateway;

use InvalidArgumentException;
use Webong\Gateway\Protocols\HttpTransport;
use Webong\Proxy\Endpoint;

/** @internal Adapts endpoint-owned Proxy profiles to Gateway's wire contract. */
final class EndpointHttpTransportResolver
{
    public function resolve(Endpoint $endpoint, string $profileName): HttpTransport
    {
        $profile = $endpoint->profile($profileName);
        if ($profile === null) {
            throw new InvalidArgumentException("Proxy profile [{$profileName}] is not active for endpoint [{$endpoint->record->endpoint_key}].");
        }
        if (! in_array($profile->driver, ['guzzle', 'http'], true)) {
            throw new InvalidArgumentException("Proxy profile [{$profileName}] does not use an HTTP-compatible driver.");
        }

        $configuration = $profile->configuration;
        $options = $configuration['options'] ?? [];
        if (! is_array($options)) {
            throw new InvalidArgumentException("Proxy profile [{$profileName}] options must be an object.");
        }

        [$httpProxy, $httpsProxy, $noProxy] = $this->proxy($configuration['proxy'] ?? null, $profileName);

        return new HttpTransport(
            httpProxy: $httpProxy,
            httpsProxy: $httpsProxy,
            noProxy: $noProxy,
            streamHeaders: $this->streamHeaders($options, $profileName),
            connectTimeoutSeconds: $this->timeout($options, 'connect_timeout', $profileName),
            requestTimeoutSeconds: $this->timeout($options, 'timeout', $profileName),
        );
    }

    /** @param array<string, mixed> $options @return array<string, list<string>> */
    private function streamHeaders(array $options, string $profileName): array
    {
        $headers = $options['headers'] ?? [];
        if (! is_array($headers)) {
            throw new InvalidArgumentException("Proxy profile [{$profileName}] headers must be an object.");
        }

        $normalized = [];
        foreach ($headers as $name => $values) {
            if (! is_string($name) || preg_match('/^[!#$%&\'*+.^_`|~0-9A-Za-z-]+$/', $name) !== 1 || $this->reservedStreamHeader($name)) {
                throw new InvalidArgumentException("Proxy profile [{$profileName}] contains an invalid header name.");
            }
            $values = is_string($values) ? [$values] : $values;
            if (! is_array($values)) {
                throw new InvalidArgumentException("Proxy profile [{$profileName}] header values must be strings or lists.");
            }
            foreach ($values as $value) {
                if (! is_string($value) || str_contains($value, "\r") || str_contains($value, "\n")) {
                    throw new InvalidArgumentException("Proxy profile [{$profileName}] contains an invalid header value.");
                }
            }
            $normalized[$name] = array_values($values);
        }

        return $normalized;
    }

    private function reservedStreamHeader(string $name): bool
    {
        return in_array(strtolower($name), ['connection', 'content-length', 'host', 'last-event-id', 'transfer-encoding', 'upgrade'], true);
    }

    /** @return array{?string, ?string, list<string>} */
    private function proxy(mixed $proxy, string $profileName): array
    {
        if ($proxy === null) {
            return [null, null, []];
        }
        if (is_string($proxy) && $proxy !== '') {
            return [$proxy, $proxy, []];
        }
        if (! is_array($proxy) || $proxy === []) {
            throw new InvalidArgumentException("Proxy profile [{$profileName}] proxy must be a non-empty string or map.");
        }

        $unsupported = array_diff(array_keys($proxy), ['http', 'https', 'no']);
        if ($unsupported !== []) {
            throw new InvalidArgumentException("Proxy profile [{$profileName}] contains unsupported proxy schemes.");
        }
        $noProxy = $proxy['no'] ?? [];
        if (! is_array($noProxy)) {
            throw new InvalidArgumentException("Proxy profile [{$profileName}] no-proxy hosts must be a list.");
        }
        foreach (['http', 'https'] as $scheme) {
            if (array_key_exists($scheme, $proxy) && ! is_string($proxy[$scheme])) {
                throw new InvalidArgumentException("Proxy profile [{$profileName}] {$scheme} proxy must be a string.");
            }
        }

        return [
            $proxy['http'] ?? null,
            $proxy['https'] ?? null,
            array_values($noProxy),
        ];
    }

    /** @param array<string, mixed> $options */
    private function timeout(array $options, string $key, string $profileName): ?float
    {
        if (! array_key_exists($key, $options)) {
            return null;
        }
        if (! is_int($options[$key]) && ! is_float($options[$key])) {
            throw new InvalidArgumentException("Proxy profile [{$profileName}] {$key} must be numeric.");
        }

        return (float) $options[$key];
    }
}
