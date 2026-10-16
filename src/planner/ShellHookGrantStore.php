<?php

declare(strict_types=1);

namespace Webong\Gateway;

use InvalidArgumentException;
use stdClass;
use Webong\Gateway\Resources\PlannerResourceStore;

/** Exact endpoint/subscriber/hook grants in the shared Planner collection. */
final readonly class ShellHookGrantStore
{
    private const KIND = 'shell.hook-grant';

    public function __construct(private PlannerResourceStore $resources) {}

    /** @return list<stdClass> */
    public function all(): array
    {
        return array_map($this->hydrate(...), $this->resources->all(self::KIND));
    }

    public function find(string $id): ?stdClass
    {
        $row = $this->resources->find(self::KIND, $id);

        return $row === null ? null : $this->hydrate($row);
    }

    public function allowed(string $endpointKey, string $subscriberId, string $hook): bool
    {
        $row = $this->resources->byKey(self::KIND, $this->key($endpointKey, $subscriberId, $hook));

        return $row !== null && $row->enabled;
    }

    /** @param array<string, mixed> $input */
    public function create(array $input): stdClass
    {
        $this->validate($input);

        return $this->hydrate($this->resources->create(self::KIND,
            $this->key($input['endpoint_key'], $input['subscriber_id'], $input['hook']),
            $this->document($input), $input['enabled'] ?? true));
    }

    /** @param array<string, mixed> $input */
    public function replace(string $id, array $input, int $revision): ?stdClass
    {
        $this->validate($input);
        $row = $this->resources->replace(self::KIND, $id,
            $this->key($input['endpoint_key'], $input['subscriber_id'], $input['hook']),
            $this->document($input), $input['enabled'] ?? true, $revision);

        return $row === null ? null : $this->hydrate($row);
    }

    public function delete(string $id, int $revision): bool
    {
        return $this->resources->delete(self::KIND, $id, $revision);
    }

    private function key(string $endpointKey, string $subscriberId, string $hook): string
    {
        return json_encode([$endpointKey, $subscriberId, $hook], JSON_THROW_ON_ERROR);
    }

    /** @param array<string, mixed> $input
     * @return array{endpoint_key: string, subscriber_id: string, hook: string}
     */
    private function document(array $input): array
    {
        return ['endpoint_key' => $input['endpoint_key'], 'subscriber_id' => $input['subscriber_id'],
            'hook' => $input['hook']];
    }

    private function hydrate(stdClass $row): stdClass
    {
        foreach ($row->document as $name => $value) {
            $row->{$name} = $value;
        }

        return $row;
    }

    /** @param array<string, mixed> $input */
    private function validate(array $input): void
    {
        if (array_diff(array_keys($input), ['endpoint_key', 'subscriber_id', 'hook', 'enabled']) !== [] ||
            ! is_string($input['endpoint_key'] ?? null) || $input['endpoint_key'] === '' ||
            strlen($input['endpoint_key']) > 255 ||
            ! is_string($input['subscriber_id'] ?? null) || $input['subscriber_id'] === '' ||
            strlen($input['subscriber_id']) > 255 ||
            ! is_string($input['hook'] ?? null) ||
            (array_key_exists('enabled', $input) && ! is_bool($input['enabled']))) {
            throw new InvalidArgumentException('Invalid Shell hook grant.');
        }
        ShellSubscriptionTarget::assertHook($input['hook']);
    }
}
