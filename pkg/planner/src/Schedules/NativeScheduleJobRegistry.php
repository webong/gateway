<?php

declare(strict_types=1);

namespace Webong\Gateway\Schedules;

use Closure;
use InvalidArgumentException;

/** Planner Extensions register trusted jobs that share Planner's schedule lease loop. */
final class NativeScheduleJobRegistry
{
    /** @var array<string, string> */
    private array $jobs = [];

    /** @var array<string, Closure(): array<string, int>> */
    private array $notifications = [];

    public function registerJob(string $publicName, string $language): void
    {
        if ($publicName === '' || ! str_starts_with($language, 'native.')) {
            throw new InvalidArgumentException('Native schedule jobs require a public name and native language.');
        }

        $this->jobs[$publicName] = $language;
    }

    /** @return list<string> */
    public function names(): array
    {
        return array_keys($this->jobs);
    }

    /** @param list<string> $requested @return list<string> */
    public function languages(array $requested): array
    {
        return array_values(array_map(fn (string $name): string => $this->jobs[$name], $requested));
    }

    /** @param Closure(): array<string, int> $dispatcher */
    public function registerNotification(string $name, Closure $dispatcher): void
    {
        $this->notifications[$name] = $dispatcher;
    }

    /** @return array<string, array<string, int>> */
    public function dispatchNotifications(): array
    {
        $results = [];
        foreach ($this->notifications as $name => $dispatcher) {
            $results[$name] = $dispatcher();
        }

        return $results;
    }
}
