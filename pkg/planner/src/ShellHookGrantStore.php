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

    /** @param array<string, mixed>|null $ssh */
    public function allowed(string $endpointKey, string $subscriberId, string $hook, ?array $ssh = null): bool
    {
        $row = $this->resources->byKey(self::KIND, $this->key($endpointKey, $subscriberId, $hook, $ssh));

        return $row !== null && $row->enabled;
    }

    /** @param array<string, mixed> $input */
    public function create(array $input): stdClass
    {
        $this->validate($input);

        return $this->hydrate($this->resources->create(self::KIND,
            $this->key($input['endpoint_key'], $input['subscriber_id'], $input['hook'], $this->sshIdentity($input)),
            $this->document($input), $input['enabled'] ?? true));
    }

    /** @param array<string, mixed> $input */
    public function replace(string $id, array $input, int $revision): ?stdClass
    {
        $this->validate($input);
        $row = $this->resources->replace(self::KIND, $id,
            $this->key($input['endpoint_key'], $input['subscriber_id'], $input['hook'], $this->sshIdentity($input)),
            $this->document($input), $input['enabled'] ?? true, $revision);

        return $row === null ? null : $this->hydrate($row);
    }

    public function delete(string $id, int $revision): bool
    {
        return $this->resources->delete(self::KIND, $id, $revision);
    }

    /** @param array<string, mixed>|null $ssh */
    private function key(string $endpointKey, string $subscriberId, string $hook, ?array $ssh = null): string
    {
        return json_encode($ssh === null
            ? [$endpointKey, $subscriberId, $hook]
            : [$endpointKey, $subscriberId, $hook, 'ssh', $ssh['host'], $ssh['port'], $ssh['user'], $ssh['host_public_key']], JSON_THROW_ON_ERROR);
    }

    /** @param array<string, mixed> $input
     * @return array<string, mixed>
     */
    private function document(array $input): array
    {
        return ['endpoint_key' => $input['endpoint_key'], 'subscriber_id' => $input['subscriber_id'],
            'hook' => $input['hook'], 'mode' => $input['mode'] ?? 'local',
            ...($this->sshIdentity($input) ?? [])];
    }

    /** @param array<string, mixed> $input @return array<string, mixed>|null */
    private function sshIdentity(array $input): ?array
    {
        if (($input['mode'] ?? 'local') !== 'ssh') {
            return null;
        }

        return ['host' => $input['host'], 'port' => $input['port'] ?? 22, 'user' => $input['user'],
            'host_public_key' => $input['host_public_key']];
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
        if (array_diff(array_keys($input), ['endpoint_key', 'subscriber_id', 'hook', 'enabled', 'mode', 'host', 'port', 'user', 'host_public_key']) !== [] ||
            ! is_string($input['endpoint_key'] ?? null) || $input['endpoint_key'] === '' ||
            strlen($input['endpoint_key']) > 255 ||
            ! is_string($input['subscriber_id'] ?? null) || $input['subscriber_id'] === '' ||
            strlen($input['subscriber_id']) > 255 ||
            ! is_string($input['hook'] ?? null) ||
            (array_key_exists('enabled', $input) && ! is_bool($input['enabled']))) {
            throw new InvalidArgumentException('Invalid Shell hook grant.');
        }
        ShellSubscriptionTarget::assertHook($input['hook']);
        $mode = $input['mode'] ?? 'local';
        if (! in_array($mode, ['local', 'ssh'], true)
            || ($mode === 'local' && array_intersect(array_keys($input), ['host', 'port', 'user', 'host_public_key']) !== [])
            || ($mode === 'ssh' && (! is_string($input['host'] ?? null) || $input['host'] === ''
                || ! is_int($input['port'] ?? 22) || ($input['port'] ?? 22) < 1 || ($input['port'] ?? 22) > 65535
                || ! is_string($input['user'] ?? null) || $input['user'] === ''
                || ! is_string($input['host_public_key'] ?? null) || $input['host_public_key'] === ''))) {
            throw new InvalidArgumentException('Invalid Shell hook grant.');
        }
    }
}
