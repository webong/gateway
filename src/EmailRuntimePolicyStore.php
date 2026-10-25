<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use InvalidArgumentException;
use stdClass;
use Webong\Gateway\Resources\PlannerResourceStore;

/** Operator-owned Email behavior; installed transports and safety ceilings stay in deployment config. */
final readonly class EmailRuntimePolicyStore
{
    private const KIND = 'email.runtime-policy';

    private const KEY = 'default';

    private const DEFAULTS = [
        'connected_smtp_enabled' => false,
        'http_submission_enabled' => false,
        'dmarc_enforce_enabled' => false,
        'verification_ttl_seconds' => 86400,
        'submission_usage_retention_days' => 90,
    ];

    public function __construct(private PlannerResourceStore $resources) {}

    public function find(): ?stdClass
    {
        return $this->resources->byKey(self::KIND, self::KEY);
    }

    /** @return array<string, bool|int> */
    public function current(): array
    {
        $row = $this->find();

        return $row !== null && $row->enabled ? $row->document : self::DEFAULTS;
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
     * @return array<string, bool|int>
     */
    private function document(array $input): array
    {
        return array_replace(self::DEFAULTS, array_diff_key($input, ['enabled' => true]));
    }

    /** @param array<string, mixed> $input */
    private function validate(array $input): void
    {
        if (array_diff(array_keys($input), [...array_keys(self::DEFAULTS), 'enabled']) !== []
            || (array_key_exists('enabled', $input) && ! is_bool($input['enabled']))) {
            throw new InvalidArgumentException('Invalid Email runtime policy.');
        }
        foreach (['connected_smtp_enabled', 'http_submission_enabled', 'dmarc_enforce_enabled'] as $key) {
            if (array_key_exists($key, $input) && ! is_bool($input[$key])) {
                throw new InvalidArgumentException('Invalid Email runtime policy.');
            }
        }
        $ttl = $input['verification_ttl_seconds'] ?? self::DEFAULTS['verification_ttl_seconds'];
        $retention = $input['submission_usage_retention_days'] ?? self::DEFAULTS['submission_usage_retention_days'];
        if (! is_int($ttl) || $ttl < 0 || $ttl > 604800
            || ! is_int($retention) || $retention < 1 || $retention > 3650) {
            throw new InvalidArgumentException('Invalid Email runtime policy.');
        }
    }
}
