<?php

declare(strict_types=1);

namespace Webong\Gateway\Events;

use InvalidArgumentException;
use stdClass;
use Webong\Gateway\Resources\PlannerResourceStore;

/** Opt-in Web event history and retention, shared across Planner workers. */
final readonly class WebEventPolicyStore
{
    private const KIND = 'web.event-policy';

    private const KEY = 'default';

    public function __construct(private PlannerResourceStore $resources) {}

    public function find(): ?stdClass
    {
        return $this->resources->byKey(self::KIND, self::KEY);
    }

    /** @return array{enabled: bool, retention_days: int} */
    public function current(): array
    {
        $row = $this->find();

        return $row === null ? ['enabled' => false, 'retention_days' => 30]
            : ['enabled' => $row->enabled, 'retention_days' => $row->document['retention_days']];
    }

    /** @param array<string, mixed> $input */
    public function create(array $input): stdClass
    {
        $this->validate($input);

        return $this->resources->create(self::KIND, self::KEY, $this->document($input), $input['enabled'] ?? true);
    }

    /** @param array<string, mixed> $input */
    public function replace(array $input, int $revision): ?stdClass
    {
        $this->validate($input);
        $row = $this->find();

        return $row === null ? null : $this->resources->replace(self::KIND, $row->id, self::KEY,
            $this->document($input), $input['enabled'] ?? true, $revision);
    }

    public function delete(int $revision): bool
    {
        $row = $this->find();

        return $row !== null && $this->resources->delete(self::KIND, $row->id, $revision);
    }

    /** @param array<string, mixed> $input
     * @return array{retention_days: int}
     */
    private function document(array $input): array
    {
        return ['retention_days' => $input['retention_days'] ?? 30];
    }

    /** @param array<string, mixed> $input */
    private function validate(array $input): void
    {
        $days = $input['retention_days'] ?? 30;
        if (array_diff(array_keys($input), ['enabled', 'retention_days']) !== []
            || (array_key_exists('enabled', $input) && ! is_bool($input['enabled']))
            || ! is_int($days) || $days < 1 || $days > 3650) {
            throw new InvalidArgumentException('Invalid Web event policy.');
        }
    }
}
