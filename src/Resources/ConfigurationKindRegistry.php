<?php

declare(strict_types=1);

namespace Webong\Gateway\Resources;

/** Publicly manageable document kinds; new kinds must be registered deliberately. */
final class ConfigurationKindRegistry
{
    /** @var array<string, true> */
    private array $kinds = [
        'extension' => true,
        'schedule.notification' => true,
        'shell.hook-grant' => true,
        'shell.ssh-execution' => true,
    ];

    public function register(string ...$kinds): void
    {
        foreach ($kinds as $kind) {
            if ($kind === '' || preg_match('/\A[a-z][a-z0-9]*(?:[.-][a-z][a-z0-9]*)*\z/D', $kind) !== 1) {
                throw new \InvalidArgumentException("Invalid Planner resource kind [{$kind}].");
            }

            $this->kinds[$kind] = true;
        }
    }

    /** @return list<string> */
    public function all(): array
    {
        $kinds = array_keys($this->kinds);
        sort($kinds);

        return $kinds;
    }

    public function contains(string $kind): bool
    {
        return in_array($kind, $this->all(), true);
    }
}
