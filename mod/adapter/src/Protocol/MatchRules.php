<?php

declare(strict_types=1);

namespace Webong\Gateway\Protocols;

use InvalidArgumentException;
use Webong\WorkFlow\Rules\RuleSet;

/** Gateway event-field policy around the reusable rule definition. */
final readonly class MatchRules
{
    public const VERSION = RuleSet::VERSION;
    public const METADATA_KEY = '_gateway_match';

    /** @var array<string, list<string>> */
    public array $rules;
    public string $version;

    private function __construct(private RuleSet $definition)
    {
        $this->rules = $definition->rules;
        $this->version = $definition->version;
    }

    /** @param array<string, string|list<string>> $rules */
    public static function make(array $rules): self
    {
        $normalized = [];
        foreach ($rules as $field => $items) {
            if (! is_string($field)) {
                throw new InvalidArgumentException('Match-rule field names must be strings.');
            }
            $field = self::normalizeField($field);
            if (array_key_exists($field, $normalized)) {
                throw new InvalidArgumentException("Match-rule field [{$field}] is defined more than once after normalization.");
            }
            $normalized[$field] = $items;
        }
        return new self(RuleSet::make($normalized));
    }

    public static function any(): self { return new self(RuleSet::any()); }

    /** @param array<string, mixed> $payload */
    public static function fromArray(array $payload): self
    {
        $definition = RuleSet::fromArray($payload);
        return self::make($definition->rules);
    }

    public function matchesEverything(): bool { return $this->definition->matchesEverything(); }

    /** @return array{version: string, rules: array<string, list<string>>} */
    public function toArray(): array { return $this->definition->toArray(); }

    public function toRuleSet(): RuleSet { return $this->definition; }

    public static function normalizeFieldName(string $field): string { return self::normalizeField($field); }

    private static function normalizeField(string $field): string
    {
        $field = trim($field);

        if (strlen($field) > 512) {
            throw new InvalidArgumentException('Match-rule field name is too long.');
        }

        if (in_array($field, ['protocol', 'event', 'session_id', 'host', 'route', 'headers', 'payload', 'attributes', 'method', 'scheme', 'path', 'query', 'body'], true)) {
            return $field;
        }

        foreach (['headers.', 'payload.', 'attributes.', 'query.', 'body.'] as $prefix) {
            if (! str_starts_with($field, $prefix)) {
                continue;
            }

            $path = substr($field, strlen($prefix));
            if ($path === '' || str_contains($path, '..')) {
                break;
            }

            $segments = explode('.', $path);
            foreach ($segments as $segment) {
                if ($segment === '' || preg_match('/^[A-Za-z0-9_*\-]+$/', $segment) !== 1) {
                    throw new InvalidArgumentException("Match-rule field [{$field}] is invalid.");
                }
            }

            if ($prefix === 'headers.') {
                $path = strtolower($path);
            }

            return $prefix.$path;
        }

        throw new InvalidArgumentException("Match-rule field [{$field}] is outside the gateway event matching document.");
    }
}
