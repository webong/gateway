<?php

declare(strict_types=1);

namespace Webong\Gateway\Protocols;

use InvalidArgumentException;
use Webong\WorkFlow\Rules\RuleSet;
use Webong\WorkFlow\Conditions\ConditionCompiler;

/** Gateway event-field policy around the reusable rule definition. */
final readonly class MatchRules
{
    public const VERSION = RuleSet::VERSION;
    public const METADATA_KEY = '_gateway_match';

    /** @var array<string, list<string>> */
    public array $rules;
    public string $version;

    private function __construct(private ?RuleSet $definition, private ?array $condition = null)
    {
        $this->rules = $definition?->rules ?? [];
        $this->version = self::VERSION;
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
        if (($payload['version'] ?? null) !== self::VERSION) {
            throw new InvalidArgumentException('Unsupported gateway match version.');
        }
        if (! isset($payload['syntax']) && array_diff(array_keys($payload), ['version', 'rules']) === []) {
            return self::make(RuleSet::fromArray($payload)->rules);
        }
        $remaining = ConditionCompiler::MAX_NODES;
        $condition = self::normalizeCondition($payload, 0, $remaining);
        if (isset($condition['rules'])) { return self::make($condition['rules']); }
        return new self(null, $condition);
    }

    public function matchesEverything(): bool { return $this->definition?->matchesEverything() ?? false; }
    public function isRules(): bool { return $this->definition !== null; }

    /** @return array<string, mixed> */
    public function toArray(): array { return $this->condition ?? $this->definition->toArray(); }

    public function toRuleSet(): RuleSet
    {
        return $this->definition ?? throw new InvalidArgumentException('Replace jq/composite conditions instead of patching rule fields.');
    }

    /** @return list<array<string, list<string>>> */
    public function ruleLeaves(): array
    {
        $leaves = [];
        $visit = static function (array $node) use (&$visit, &$leaves): void {
            if (isset($node['rules'])) { $leaves[] = $node['rules']; return; }
            foreach (['all', 'any'] as $operator) {
                foreach ($node[$operator] ?? [] as $child) { $visit($child); }
            }
            if (isset($node['not'])) { $visit($node['not']); }
        };
        $visit($this->toArray());
        return $leaves;
    }

    /** Validate Gateway's transport shape and normalize every rules leaf.
     * Driver compilation remains owned by the host's shared authoring/runtime compiler.
     * @param array<mixed> $node @return array<string, mixed>
     */
    private static function normalizeCondition(array $node, int $depth, int &$remaining): array
    {
        if ($depth > ConditionCompiler::MAX_DEPTH || --$remaining < 0) {
            throw new InvalidArgumentException('Match condition exceeds its tree limits.');
        }
        if (array_key_exists('version', $node) && $node['version'] !== self::VERSION) {
            throw new InvalidArgumentException('Unsupported gateway match version.');
        }
        $operators = array_intersect(['all', 'any', 'not'], array_keys($node));
        if ($operators !== []) {
            if (count($operators) !== 1) { throw new InvalidArgumentException('Match condition requires exactly one operator.'); }
            $operator = array_values($operators)[0];
            if (array_diff(array_keys($node), ['version', $operator]) !== []) { throw new InvalidArgumentException('Match condition contains unsupported properties.'); }
            if ($operator === 'not') {
                if (! is_array($node['not'])) { throw new InvalidArgumentException('Match not requires a condition document.'); }
                $node['not'] = self::normalizeCondition($node['not'], $depth + 1, $remaining);
            } else {
                $children = $node[$operator];
                if (! is_array($children) || ! array_is_list($children) || $children === [] || count($children) > ConditionCompiler::MAX_CHILDREN) {
                    throw new InvalidArgumentException('Match composition requires a bounded non-empty list.');
                }
                foreach ($children as $index => $child) {
                    if (! is_array($child)) { throw new InvalidArgumentException('Match child must be a condition document.'); }
                    $node[$operator][$index] = self::normalizeCondition($child, $depth + 1, $remaining);
                }
            }
            return $node;
        }
        $syntax = array_key_exists('syntax', $node) ? $node['syntax'] : (array_key_exists('rules', $node) ? 'rules' : null);
        $allowed = $syntax === 'rules' ? ['version', 'syntax', 'rules'] : ['version', 'syntax', 'expression'];
        if (array_diff(array_keys($node), $allowed) !== []) { throw new InvalidArgumentException('Match leaf contains unsupported properties.'); }
        if ($syntax === 'rules' && is_array($node['rules'] ?? null)) {
            $node['rules'] = self::make($node['rules'])->rules;
        } elseif ($syntax !== 'jq' || ! is_string($node['expression'] ?? null) || trim($node['expression']) === '') {
            throw new InvalidArgumentException('Match leaf requires rules or a jq expression.');
        }
        return $node;
    }

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
