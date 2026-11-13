<?php

declare(strict_types=1);

namespace Webong\Gateway\Web\Events;

use InvalidArgumentException;
use Webong\Gateway\Laravel\Resources\PlannerResourceStore;
use stdClass;

/** Opt-in Web event capture policy. Shared ledger retention is Planner-owned. */
final readonly class WebEventPolicyStore
{
    private const KIND = 'web.event-policy';

    private const KEY = 'default';

    public function __construct(private PlannerResourceStore $resources) {}

    public function find(): ?stdClass
    {
        return $this->resources->byKey(self::KIND, self::KEY);
    }

    /** @return array{enabled: bool} */
    public function current(): array
    {
        $row = $this->find();

        return ['enabled' => $row?->enabled ?? false];
    }

    /** @deprecated Read-only compatibility for installations moving retention to Planner's generic setting. */
    public function legacyRetentionDays(): ?int
    {
        $row = $this->find();
        $days = $row?->document['retention_days'] ?? null;
        return $row !== null && is_int($days) && $days >= 1 && $days <= 3650 ? $days : null;
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
            $this->document($input, $row), $input['enabled'] ?? true, $revision);
    }

    public function delete(int $revision): bool
    {
        $row = $this->find();

        return $row !== null && $this->resources->delete(self::KIND, $row->id, $revision);
    }

    /** @param array<string, mixed> $input
     * @return array{}
     */
    private function document(array $input, ?stdClass $existing = null): array
    {
        $legacyDays = $existing?->document['retention_days'] ?? null;
        return is_int($legacyDays) && $legacyDays >= 1 && $legacyDays <= 3650
            ? ['retention_days' => $legacyDays] : [];
    }

    /** @param array<string, mixed> $input */
    private function validate(array $input): void
    {
        if (array_diff(array_keys($input), ['enabled']) !== []
            || (array_key_exists('enabled', $input) && ! is_bool($input['enabled']))) {
            throw new InvalidArgumentException('Invalid Web event policy.');
        }
    }
}
