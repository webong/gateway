<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\MatchRules;

final readonly class RequestMatcher
{
    /** @var array<string, true> */
    private const HTTP_ONLY_FIELDS = [
        'method' => true,
        'scheme' => true,
        'path' => true,
        'query' => true,
        'body' => true,
    ];

    public function __construct(
        private ValidationFactory $validator,
    ) {
    }

    public function matches(IngressRequest $request, MatchRules $match): bool
    {
        if ($match->matchesEverything()) {
            return true;
        }

        if ($request->protocol !== 'http') {
            foreach (array_keys($match->rules) as $field) {
                $root = explode('.', $field, 2)[0];
                if (isset(self::HTTP_ONLY_FIELDS[$root])) {
                    return false;
                }
            }
        }

        return $this->validator->make(
            $this->matchingDocument($request),
            $match->rules,
        )->passes();
    }

    /** @return array<string, mixed> */
    public function matchingDocument(IngressRequest $request): array
    {
        $payload = $this->payload($request->body);
        $document = [
            'protocol' => $request->protocol,
            'event' => $request->event,
            'session_id' => $request->sessionId,
            'host' => $request->host,
            'route' => $request->path,
            'headers' => $this->headers($request->headers),
            'payload' => $payload,
            'attributes' => $request->attributes,
        ];

        // Protocol events share this adapter, but its fallback method/scheme are not ingress facts.
        if ($request->protocol === 'http') {
            parse_str($request->rawQuery, $query);

            $document['method'] = $request->method;
            $document['scheme'] = $request->scheme;
            $document['path'] = $request->path;
            $document['query'] = $query;
            $document['body'] = $payload;
        }

        return $document;
    }

    /**
     * @param array<string, list<string>> $headers
     * @return array<string, string|list<string>>
     */
    private function headers(array $headers): array
    {
        $grouped = [];

        foreach ($headers as $name => $values) {
            $name = strtolower($name);
            $grouped[$name] = [...($grouped[$name] ?? []), ...array_values($values)];
        }

        $normalized = [];
        foreach ($grouped as $name => $values) {
            $normalized[$name] = count($values) === 1 ? $values[0] : $values;
        }

        return $normalized;
    }

    private function payload(string $body): mixed
    {
        if ($body === '') {
            return [];
        }

        try {
            return json_decode($body, true, 512, JSON_THROW_ON_ERROR);
        } catch (\JsonException) {
            return $body;
        }
    }
}
