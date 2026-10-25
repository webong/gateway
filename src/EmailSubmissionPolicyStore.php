<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use InvalidArgumentException;
use stdClass;
use Webong\Gateway\Resources\PlannerResourceStore;

/** Shared account and sender-domain quota defaults, not a transport ceiling. */
final readonly class EmailSubmissionPolicyStore
{
    private const KIND = 'email.submission-policy';

    private const KEY = 'default';

    private const FIELDS = ['daily_limit', 'daily_byte_limit', 'domain_daily_limit', 'domain_daily_byte_limit'];

    public function __construct(private PlannerResourceStore $resources) {}

    public function find(): ?stdClass
    {
        return $this->resources->byKey(self::KIND, self::KEY);
    }

    /** @return array{daily_limit: int, daily_byte_limit: int, domain_daily_limit: int, domain_daily_byte_limit: int} */
    public function limits(): array
    {
        $row = $this->find();

        return $row !== null && $row->enabled ? $row->document : $this->document([]);
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
        if ($row === null) {
            return null;
        }

        return $this->resources->replace(self::KIND, $row->id, self::KEY,
            $this->document($input), $input['enabled'] ?? true, $revision);
    }

    public function delete(int $revision): bool
    {
        $row = $this->find();

        return $row !== null && $this->resources->delete(self::KIND, $row->id, $revision);
    }

    /** @param array<string, mixed> $input
     * @return array{daily_limit: int, daily_byte_limit: int, domain_daily_limit: int, domain_daily_byte_limit: int}
     */
    private function document(array $input): array
    {
        return ['daily_limit' => $input['daily_limit'] ?? 0,
            'daily_byte_limit' => $input['daily_byte_limit'] ?? 0,
            'domain_daily_limit' => $input['domain_daily_limit'] ?? 0,
            'domain_daily_byte_limit' => $input['domain_daily_byte_limit'] ?? 0];
    }

    /** @param array<string, mixed> $input */
    private function validate(array $input): void
    {
        if (array_diff(array_keys($input), [...self::FIELDS, 'enabled']) !== [] ||
            (array_key_exists('enabled', $input) && ! is_bool($input['enabled']))) {
            throw new InvalidArgumentException('Invalid Email submission policy.');
        }
        foreach (self::FIELDS as $field) {
            $value = $input[$field] ?? 0;
            $maximum = str_contains($field, 'byte') ? 1_000_000_000_000 : 1_000_000;
            if (! is_int($value) || $value < 0 || $value > $maximum) {
                throw new InvalidArgumentException('Invalid Email submission policy.');
            }
        }
    }
}
