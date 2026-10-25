<?php

declare(strict_types=1);

namespace Webong\Gateway\Protocols;

use InvalidArgumentException;

final readonly class GatewayEvent
{
    /** @param array<string, list<string>> $headers @param array<string, string> $attributes @param list<string> $automationLanguages */
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
        public array $automationLanguages = [],
    ) {
        if ($this->id === '' || $this->route === '') {
            throw new InvalidArgumentException('Gateway event id and route are required.');
        }

        if ($this->protocol === Protocol::HTTP && $this->kind !== EventKind::REQUEST) {
            throw new InvalidArgumentException('HTTP gateway events must use the request kind.');
        }

        if ($this->protocol === Protocol::DNS && $this->kind !== EventKind::QUERY) {
            throw new InvalidArgumentException('DNS gateway events must use the query kind.');
        }

        if ($this->protocol === Protocol::POP3 && ! in_array($this->kind, [EventKind::AUTHENTICATE, EventKind::CONNECT, EventKind::CLOSE], true)) {
            throw new InvalidArgumentException('POP3 gateway events must authenticate, connect, or close.');
        }
        if ($this->protocol === Protocol::IMAP && ! in_array($this->kind, [EventKind::AUTHENTICATE, EventKind::CONNECT, EventKind::CLOSE], true)) {
            throw new InvalidArgumentException('IMAP gateway events must authenticate, connect, or close.');
        }

        self::assertAutomationLanguages($this->automationLanguages);
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
            automationLanguages: self::automationLanguages($payload['runtime'] ?? []),
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
            'runtime' => $this->automationLanguages === [] ? [] : [
                'automation_languages' => $this->automationLanguages,
            ],
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

    /** @return list<string> */
    private static function automationLanguages(mixed $runtime): array
    {
        if (! is_array($runtime)) {
            throw new InvalidArgumentException('Gateway event runtime capabilities must be an object.');
        }

        $languages = $runtime['automation_languages'] ?? [];
        if (! is_array($languages) || ! array_is_list($languages)) {
            throw new InvalidArgumentException('Gateway event automation languages must be a list.');
        }

        self::assertAutomationLanguages($languages);

        return array_values(array_unique($languages));
    }

    /** @param list<mixed> $languages */
    private static function assertAutomationLanguages(array $languages): void
    {
        if (count($languages) > 32) {
            throw new InvalidArgumentException('Gateway event automation language list is too large.');
        }
        foreach ($languages as $language) {
            if (! is_string($language) || preg_match('/^[a-z][a-z0-9_-]*$/', $language) !== 1) {
                throw new InvalidArgumentException('Gateway event automation language identifiers are invalid.');
            }
        }
    }
}
