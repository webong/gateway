<?php

declare(strict_types=1);

namespace Webong\Gateway\Migrations;

use InvalidArgumentException;
use LogicException;

/** Package-owned schema contributions; each native runner retains its own history. */
final class MigrationRegistry
{
    /** @var array<string, array{path: string, runner: string, before_core: bool}> */
    private array $sets = [];

    public function __construct(?string $corePath = null)
    {
        $this->register('gateway', $corePath ?? dirname(__DIR__, 2).'/migrations');
    }

    public function register(string $owner, string $path): void
    {
        $this->add($owner, $path, 'phinx', false);
    }

    public function registerLaravel(string $owner, string $path, bool $beforeCore = false): void
    {
        $this->add($owner, $path, 'laravel', $beforeCore);
    }

    public function corePath(): string
    {
        return $this->sets['gateway']['path'];
    }

    /** @return list<string> */
    public function phinxPaths(): array
    {
        return $this->paths('phinx');
    }

    /** @return list<string> */
    public function laravelPaths(?bool $beforeCore = null): array
    {
        return $this->paths('laravel', $beforeCore);
    }

    private function add(string $owner, string $path, string $runner, bool $beforeCore): void
    {
        $canonical = realpath($path);
        if (trim($owner) === '' || $canonical === false || ! is_dir($canonical)) {
            throw new InvalidArgumentException('Migration contributions need a package owner and an existing directory.');
        }
        $set = ['path' => $canonical, 'runner' => $runner, 'before_core' => $beforeCore];
        if (isset($this->sets[$owner])) {
            if ($this->sets[$owner] !== $set) {
                throw new LogicException("Migration owner [{$owner}] is already registered differently.");
            }
            return;
        }
        foreach ($this->sets as $existing) {
            if ($existing['path'] === $canonical) {
                throw new LogicException('A migration directory cannot have multiple owners.');
            }
        }
        $this->sets[$owner] = $set;
    }

    /** @return list<string> */
    private function paths(string $runner, ?bool $beforeCore = null): array
    {
        return array_values(array_map(
            static fn (array $set): string => $set['path'],
            array_filter($this->sets, static fn (array $set): bool => $set['runner'] === $runner
                && ($beforeCore === null || $set['before_core'] === $beforeCore)),
        ));
    }
}
