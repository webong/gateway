<?php

declare(strict_types=1);

namespace Webong\Gateway\Schedules;

use InvalidArgumentException;
use stdClass;
use Webong\Gateway\Resources\PlannerResourceStore;

/** A singleton operator destination, selected only through Planner. */
final readonly class ScheduleNotificationConfig
{
    private const KIND = 'schedule.notification';

    private const KEY = 'failure';

    public function __construct(private PlannerResourceStore $resources) {}

    public function find(): ?stdClass
    {
        return $this->resources->byKey(self::KIND, self::KEY);
    }

    /** @return array{url: string, token: string}|null */
    public function effective(): ?array
    {
        $row = $this->find();
        if ($row === null || ! $row->enabled || ! $this->allowed($row->document['url'])) {
            return null;
        }

        return ['url' => $row->document['url'], 'token' => $row->document['token']];
    }

    /** @param array<string, mixed> $input */
    public function create(array $input): stdClass
    {
        $this->validate($input);

        return $this->resources->create(self::KIND, self::KEY,
            ['url' => $input['url'], 'token' => $input['token'] ?? ''], $input['enabled'] ?? true);
    }

    /** @param array<string, mixed> $input */
    public function replace(array $input, int $revision): ?stdClass
    {
        $current = $this->find();
        if ($current === null) {
            return null;
        }
        $this->validate($input);

        return $this->resources->replace(self::KIND, $current->id, self::KEY,
            ['url' => $input['url'], 'token' => $input['token'] ?? $current->document['token']],
            $input['enabled'] ?? true, $revision);
    }

    public function delete(int $revision): bool
    {
        $current = $this->find();

        return $current !== null && $this->resources->delete(self::KIND, $current->id, $revision);
    }

    /** @param array<string, mixed> $input */
    private function validate(array $input): void
    {
        if (array_diff(array_keys($input), ['url', 'token', 'enabled']) !== [] ||
            ! is_string($input['url'] ?? null) || strlen($input['url']) > 2048 ||
            (array_key_exists('token', $input) && (! is_string($input['token']) || strlen($input['token']) > 4096)) ||
            (array_key_exists('enabled', $input) && ! is_bool($input['enabled'])) ||
            ! $this->allowed($input['url'])) {
            throw new InvalidArgumentException('Invalid schedule notification destination or host is not deployed.');
        }
    }

    private function allowed(string $url): bool
    {
        $parts = parse_url($url);
        if (! is_array($parts) ||
            ! is_string($parts['host'] ?? null) || isset($parts['user']) || isset($parts['pass']) ||
            isset($parts['query']) || isset($parts['fragment']) ||
            (($parts['scheme'] ?? null) !== 'https' &&
                !(($parts['scheme'] ?? null) === 'http' && $parts['host'] === '127.0.0.1')) ||
            (filter_var($parts['host'], FILTER_VALIDATE_IP) !== false && $parts['host'] !== '127.0.0.1') ||
            preg_match('/^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$/Di', $parts['host']) !== 1) {
            return false;
        }
        $allowed = array_map('strtolower', array_map('trim', explode(',',
            (string) config('gateway.schedule_notifications.allowed_hosts', ''))));

        return in_array(strtolower($parts['host']), $allowed, true);
    }
}
