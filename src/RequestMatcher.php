<?php

declare(strict_types=1);

namespace Webong\NetGateway;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Webong\NetGateway\Protocol\IngressRequest;
use Webong\NetGateway\Protocol\MatchRules;

final readonly class RequestMatcher
{
    public function __construct(
        private ValidationFactory $validator,
    ) {
    }

    public function matches(IngressRequest $request, MatchRules $match): bool
    {
        if ($match->matchesEverything()) {
            return true;
        }

        return $this->validator->make(
            $this->matchingDocument($request),
            $match->rules,
        )->passes();
    }

    /** @return array<string, mixed> */
    public function matchingDocument(IngressRequest $request): array
    {
        parse_str($request->rawQuery, $query);

        return [
            'protocol' => $request->protocol,
            'event' => $request->event,
            'session_id' => $request->sessionId,
            'method' => $request->method,
            'scheme' => $request->scheme,
            'host' => $request->host,
            'path' => $request->path,
            'headers' => $this->headers($request->headers),
            'query' => $query,
            'body' => $this->body($request->body),
        ];
    }

    /**
     * @param array<string, list<string>> $headers
     * @return array<string, string|list<string>>
     */
    private function headers(array $headers): array
    {
        $normalized = [];

        foreach ($headers as $name => $values) {
            $name = strtolower($name);
            $values = array_values($values);
            $normalized[$name] = count($values) === 1 ? $values[0] : $values;
        }

        return $normalized;
    }

    private function body(string $body): mixed
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
