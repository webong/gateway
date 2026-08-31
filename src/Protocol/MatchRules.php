<?php

declare(strict_types=1);

namespace Webong\Gateway\Protocol;

use InvalidArgumentException;

/**
 * Portable, Laravel-shaped request matching rules.
 *
 * The JSON representation is the canonical contract. Only a deliberately
 * bounded rule subset is accepted because remote subscribers must never be
 * able to resolve custom rules or trigger database, filesystem, or network IO.
 */
final readonly class MatchRules
{
    public const VERSION = 'v1';

    public const METADATA_KEY = '_gateway_match';

    private const MAX_FIELDS = 64;

    private const MAX_RULES_PER_FIELD = 16;

    private const MAX_RULE_LENGTH = 1024;

    /** @var array<string, true> */
    private const ALLOWED_RULES = [
        'accepted' => true,
        'array' => true,
        'bail' => true,
        'between' => true,
        'boolean' => true,
        'declined' => true,
        'ends_with' => true,
        'filled' => true,
        'in' => true,
        'integer' => true,
        'list' => true,
        'max' => true,
        'min' => true,
        'not_in' => true,
        'nullable' => true,
        'numeric' => true,
        'present' => true,
        'required' => true,
        'size' => true,
        'starts_with' => true,
        'string' => true,
    ];

    /** @param array<string, list<string>> $rules */
    private function __construct(
        public array $rules,
        public string $version = self::VERSION,
    ) {
    }

    /** @param array<string, string|list<string>> $rules */
    public static function make(array $rules): self
    {
        return new self(self::normalizeRules($rules));
    }

    public static function any(): self
    {
        return new self([]);
    }

    /** @param array<string, mixed> $payload */
    public static function fromArray(array $payload): self
    {
        if (array_diff(array_keys($payload), ['version', 'rules']) !== []) {
            throw new InvalidArgumentException('Match rules contain unsupported properties.');
        }

        $version = $payload['version'] ?? null;
        $rules = $payload['rules'] ?? null;

        if ($version !== self::VERSION) {
            throw new InvalidArgumentException('Unsupported gateway match-rules version.');
        }

        if (! is_array($rules)) {
            throw new InvalidArgumentException('Match rules must contain a rules object.');
        }

        return new self(self::normalizeRules($rules), self::VERSION);
    }

    public function matchesEverything(): bool
    {
        return $this->rules === [];
    }

    /** @return array{version: string, rules: array<string, list<string>>} */
    public function toArray(): array
    {
        return [
            'version' => $this->version,
            'rules' => $this->rules,
        ];
    }

    public static function normalizeFieldName(string $field): string
    {
        return self::normalizeField($field);
    }

    /**
     * @param array<string, mixed> $rules
     * @return array<string, list<string>>
     */
    private static function normalizeRules(array $rules): array
    {
        if (count($rules) > self::MAX_FIELDS) {
            throw new InvalidArgumentException('Match rules contain too many fields.');
        }

        $normalized = [];

        foreach ($rules as $field => $fieldRules) {
            if (! is_string($field)) {
                throw new InvalidArgumentException('Match-rule field names must be strings.');
            }

            $field = self::normalizeField($field);
            $items = is_string($fieldRules) ? explode('|', $fieldRules) : $fieldRules;

            if (! is_array($items) || $items === [] || count($items) > self::MAX_RULES_PER_FIELD) {
                throw new InvalidArgumentException("Match-rule field [{$field}] has an invalid rule list.");
            }

            $normalized[$field] = array_map(
                static function (mixed $rule) use ($field): string {
                    if (! is_string($rule) || $rule === '' || strlen($rule) > self::MAX_RULE_LENGTH) {
                        throw new InvalidArgumentException("Match-rule field [{$field}] contains an invalid rule.");
                    }

                    [$rawName, $parameters] = array_pad(explode(':', $rule, 2), 2, null);
                    $name = strtolower($rawName);
                    if (! isset(self::ALLOWED_RULES[$name])) {
                        throw new InvalidArgumentException("Match rule [{$name}] is not allowed.");
                    }

                    return $parameters === null ? $name : $name.':'.$parameters;
                },
                array_values($items),
            );
        }

        return $normalized;
    }

    private static function normalizeField(string $field): string
    {
        $field = trim($field);

        if (in_array($field, ['protocol', 'event', 'session_id', 'method', 'scheme', 'host', 'path', 'headers', 'query', 'body', 'attributes'], true)) {
            return $field;
        }

        foreach (['headers.', 'query.', 'body.', 'attributes.'] as $prefix) {
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

        throw new InvalidArgumentException("Match-rule field [{$field}] is outside the request matching document.");
    }
}
