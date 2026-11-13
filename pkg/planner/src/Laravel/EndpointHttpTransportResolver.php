<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel;

use InvalidArgumentException;
use Webong\Gateway\Protocols\HttpTransport;
use Webong\Proxy\Endpoint;
use Webong\Proxy\Http\HttpProfileNormalizer;

/**
 * @internal Adapts endpoint-owned Proxy profiles to Gateway's wire contract.
 *
 * Proxy owns the portable connection settings (proxy URLs, no-proxy rules and
 * timeouts): {@see HttpProfileNormalizer} validates them and defines the
 * `options.proxy`, `no` and timeout translation. Gateway owns what Proxy
 * deliberately leaves outside the profile: the profile lookup and its
 * messages, the source headers carried to a Webstream and the headers that
 * source may never set, and the Gateway wire value object.
 */
final class EndpointHttpTransportResolver
{
    private readonly HttpProfileNormalizer $profiles;

    public function __construct(?HttpProfileNormalizer $profiles = null)
    {
        $this->profiles = $profiles ?? new HttpProfileNormalizer();
    }

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
        $streamHeaders = $this->streamHeaders($options, $profileName);

        $portable = $this->portable($configuration, $profileName);

        return new HttpTransport(
            httpProxy: $portable['proxy']['http'] ?? null,
            httpsProxy: $portable['proxy']['https'] ?? null,
            noProxy: $portable['proxy']['no_proxy'] ?? [],
            streamHeaders: $streamHeaders,
            connectTimeoutSeconds: isset($portable['connect_timeout_seconds']) ? (float) $portable['connect_timeout_seconds'] : null,
            requestTimeoutSeconds: isset($portable['request_timeout_seconds']) ? (float) $portable['request_timeout_seconds'] : null,
        );
    }

    /**
     * @param  array<string, mixed>  $configuration
     * @return array{version: int, proxy?: array<string, mixed>, connect_timeout_seconds?: int|float, request_timeout_seconds?: int|float}
     */
    private function portable(array $configuration, string $profileName): array
    {
        try {
            $data = $this->profiles->normalize($configuration)->toArray();
        } catch (InvalidArgumentException $exception) {
            // Proxy's messages never contain credential values; add the profile they belong to.
            throw new InvalidArgumentException("Proxy profile [{$profileName}] is not a valid portable HTTP profile: ".$exception->getMessage(), previous: $exception);
        }
        // A present but empty proxy is a configuration mistake, not "no proxy": the
        // normalizer would turn it into a profile that silently connects directly.
        if (array_key_exists('proxy', $data) && $data['proxy'] === []) {
            throw new InvalidArgumentException("Proxy profile [{$profileName}] proxy must be a non-empty string or map.");
        }

        return $data;
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
}
