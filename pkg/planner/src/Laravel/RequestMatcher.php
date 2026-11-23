<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel;

use InvalidArgumentException;
use Psr\Log\LoggerInterface;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\MatchRules;
use Webong\WorkFlow\Conditions\ConditionAuthoringValidator;
use Webong\WorkFlow\Conditions\ConditionCompiler;
use Webong\WorkFlow\Conditions\ConditionDefinitionException;

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

    public function __construct(private ConditionCompiler $compiler, private ?LoggerInterface $logger = null) {}

    public function validate(MatchRules $match): void
    {
        $diagnostics = (new ConditionAuthoringValidator($this->compiler))->validate($match->toArray());
        if ($diagnostics !== []) { throw new InvalidArgumentException($diagnostics[0]->message); }
    }

    public function matches(IngressRequest $request, MatchRules $match): bool
    {
        if ($match->matchesEverything()) { return true; }
        if ($request->protocol !== 'http') {
            foreach ($match->ruleLeaves() as $rules) {
                foreach (array_keys($rules) as $field) {
                    if (isset(self::HTTP_ONLY_FIELDS[explode('.', $field, 2)[0]])) { return false; }
                }
            }
        }
        try {
            $result = $this->compiler->compile($match->toArray())->evaluate($this->matchingDocument($request));
            if ($result->isError()) {
                $this->logger?->warning('Gateway subscription condition did not evaluate.', ['code' => 'condition_evaluation_failed']);
            }
            return $result->matched();
        } catch (ConditionDefinitionException $exception) {
            $this->logger?->warning('Gateway subscription condition is invalid.', ['code' => $exception->diagnosticCode]);
            return false;
        }
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
