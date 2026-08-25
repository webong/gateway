<?php

declare(strict_types=1);

namespace Webong\Gateway\Protocol;

use InvalidArgumentException;

/**
 * Ordered, incremental edits for a subscription's match rules.
 *
 * Operations are applied atomically to an in-memory MatchRules value before
 * the subscription metadata is persisted.
 */
final readonly class MatchRulesPatch
{
    public const VERSION = 'v1';

    private const MAX_OPERATIONS = 128;

    /** @param list<array{op: 'add'|'remove', field: string, rules?: list<string>}> $operations */
    private function __construct(
        public array $operations = [],
    ) {
    }

    public static function make(): self
    {
        return new self();
    }

    /** @param array<string, mixed> $payload */
    public static function fromArray(array $payload): self
    {
        if (array_diff(array_keys($payload), ['version', 'operations']) !== []) {
            throw new InvalidArgumentException('Match patch contains unsupported properties.');
        }

        if (($payload['version'] ?? null) !== self::VERSION) {
            throw new InvalidArgumentException('Unsupported gateway match-patch version.');
        }

        $operations = $payload['operations'] ?? null;
        if (! is_array($operations) || ! array_is_list($operations) || $operations === []) {
            throw new InvalidArgumentException('Match patch requires an operations list.');
        }

        if (count($operations) > self::MAX_OPERATIONS) {
            throw new InvalidArgumentException('Match patch contains too many operations.');
        }

        $patch = self::make();
        foreach ($operations as $operation) {
            if (! is_array($operation)) {
                throw new InvalidArgumentException('Match-patch operations must be objects.');
            }

            if (array_diff(array_keys($operation), ['op', 'field', 'rules']) !== []) {
                throw new InvalidArgumentException('Match-patch operation contains unsupported properties.');
            }

            $op = $operation['op'] ?? null;
            $field = $operation['field'] ?? null;
            if (! is_string($op) || ! is_string($field)) {
                throw new InvalidArgumentException('Match-patch operations require string op and field properties.');
            }

            if ($op === 'add') {
                if (! array_key_exists('rules', $operation)) {
                    throw new InvalidArgumentException('Add operations require rules.');
                }

                $operationRules = $operation['rules'];
                if (! is_string($operationRules) && ! is_array($operationRules)) {
                    throw new InvalidArgumentException('Match-patch rules must be a string or list of strings.');
                }

                $patch = $patch->add($field, $operationRules);
                continue;
            }

            if ($op === 'remove') {
                $operationRules = $operation['rules'] ?? null;
                if (! is_null($operationRules) && ! is_string($operationRules) && ! is_array($operationRules)) {
                    throw new InvalidArgumentException('Match-patch rules must be a string or list of strings.');
                }

                $patch = $patch->remove($field, $operationRules);
                continue;
            }

            throw new InvalidArgumentException("Unsupported match-patch operation [{$op}].");
        }

        return $patch;
    }

    /** @param string|list<string> $rules */
    public function add(string $field, string|array $rules): self
    {
        [$field, $normalizedRules] = $this->normalize($field, $rules);

        return $this->append(['op' => 'add', 'field' => $field, 'rules' => $normalizedRules]);
    }

    /** @param string|list<string>|null $rules */
    public function remove(string $field, string|array|null $rules = null): self
    {
        if ($rules === null) {
            $operation = [
                'op' => 'remove',
                'field' => MatchRules::normalizeFieldName($field),
            ];
        } else {
            [$field, $normalizedRules] = $this->normalize($field, $rules);
            $operation = ['op' => 'remove', 'field' => $field, 'rules' => $normalizedRules];
        }

        return $this->append($operation);
    }

    public function apply(MatchRules $current): MatchRules
    {
        if ($this->operations === []) {
            throw new InvalidArgumentException('Match patch requires at least one operation.');
        }

        $rules = $current->rules;

        foreach ($this->operations as $operation) {
            $field = $operation['field'];
            if ($operation['op'] === 'add') {
                $rules[$field] = array_values(array_unique([
                    ...($rules[$field] ?? []),
                    ...$operation['rules'],
                ]));
                $rules = MatchRules::make($rules)->rules;
                continue;
            }

            if (! array_key_exists('rules', $operation)) {
                unset($rules[$field]);
                continue;
            }

            if (! isset($rules[$field])) {
                continue;
            }

            $rules[$field] = array_values(array_diff($rules[$field], $operation['rules']));
            if ($rules[$field] === []) {
                unset($rules[$field]);
            }
        }

        return MatchRules::make($rules);
    }

    /** @return array{version: string, operations: list<array{op: 'add'|'remove', field: string, rules?: list<string>}>} */
    public function toArray(): array
    {
        return [
            'version' => self::VERSION,
            'operations' => $this->operations,
        ];
    }

    /** @return array{string, list<string>} */
    private function normalize(string $field, mixed $rules): array
    {
        if (! is_string($rules) && ! is_array($rules)) {
            throw new InvalidArgumentException('Match-patch rules must be a string or list of strings.');
        }

        $normalized = MatchRules::make([$field => $rules]);
        $field = array_key_first($normalized->rules);

        if (! is_string($field)) {
            throw new InvalidArgumentException('Match-patch operation requires a field.');
        }

        return [$field, $normalized->rules[$field]];
    }

    /** @param array{op: 'add'|'remove', field: string, rules?: list<string>} $operation */
    private function append(array $operation): self
    {
        if (count($this->operations) >= self::MAX_OPERATIONS) {
            throw new InvalidArgumentException('Match patch contains too many operations.');
        }

        return new self([...$this->operations, $operation]);
    }
}
