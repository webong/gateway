<?php

declare(strict_types=1);

namespace Webong\Gateway\Events;

use InvalidArgumentException;
use stdClass;
use Webong\Gateway\Resources\PlannerResourceStore;

/** Planner-owned retention for the shared endpoint event ledger. */
final readonly class EventRetentionPolicyStore
{
    private const KIND = 'events.retention';
    private const KEY = 'default';

    public function __construct(
        private PlannerResourceStore $resources,
        private EventRetentionPolicyFallbackRegistry $fallbacks,
    ) {}

    public function find(): ?stdClass { return $this->resources->byKey(self::KIND, self::KEY); }
    public function retentionDays(): int
    {
        $row = $this->find();
        if ($row === null) {
            // Extensions may keep an older resource effective until its operator
            // creates the generic Planner-owned setting.
            return $this->fallbacks->retentionDays() ?? 30;
        }
        if (! $row->enabled) { return 30; }
        $days = $row->document['retention_days'] ?? null;
        if (! is_int($days) || $days < 1 || $days > 3650) {
            throw new InvalidArgumentException('Persisted event retention policy is invalid.');
        }
        return $days;
    }
    /** @param array<string, mixed> $input */
    public function create(array $input): stdClass
    {
        $this->validate($input);
        return $this->resources->create(self::KIND, self::KEY, ['retention_days' => $input['retention_days']], true);
    }
    /** @param array<string, mixed> $input */
    public function replace(array $input, int $revision): ?stdClass
    {
        $this->validate($input);
        $row = $this->find();
        return $row === null ? null : $this->resources->replace(self::KIND, $row->id, self::KEY,
            ['retention_days' => $input['retention_days']], true, $revision);
    }
    public function delete(int $revision): bool
    {
        $row = $this->find();
        if ($row === null || $row->revision !== $revision) {
            if ($row !== null) { throw new \Webong\Gateway\Resources\StaleResource('Planner resource revision has changed.'); }
            return false;
        }
        // Keep a disabled default marker so a legacy fallback cannot reappear after reset.
        return $this->resources->replace(self::KIND, $row->id, self::KEY,
            ['retention_days' => 30], false, $revision) !== null;
    }
    /** @param array<string, mixed> $input */
    private function validate(array $input): void
    {
        $days = $input['retention_days'] ?? null;
        if (array_diff(array_keys($input), ['retention_days']) !== [] || ! is_int($days) || $days < 1 || $days > 3650) {
            throw new InvalidArgumentException('Invalid event retention policy.');
        }
    }
}
