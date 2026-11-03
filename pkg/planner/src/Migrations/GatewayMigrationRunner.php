<?php

declare(strict_types=1);

namespace Webong\Gateway\Migrations;

use Phinx\Config\Config;
use Phinx\Migration\Manager;
use Symfony\Component\Console\Input\ArrayInput;
use Symfony\Component\Console\Output\NullOutput;

final readonly class GatewayMigrationRunner
{
    private const ENVIRONMENT = 'gateway';

    /** @param array<string, mixed> $connection */
    private function __construct(
        private array $connection,
        private string $migrationsPath,
    ) {}

    public static function sqlite(string $database, string $migrationsPath): self
    {
        return new self([
            'adapter' => 'sqlite',
            'name' => $database,
            'suffix' => '',
        ], $migrationsPath);
    }

    public function migrate(): MigrationResult
    {
        $manager = new Manager(new Config([
            'paths' => ['migrations' => $this->migrationsPath],
            'environments' => [
                'default_environment' => self::ENVIRONMENT,
                self::ENVIRONMENT => [
                    ...$this->connection,
                    'migration_table' => 'gateway_migrations',
                ],
            ],
        ]), new ArrayInput([]), new NullOutput());

        $environment = $manager->getEnvironment(self::ENVIRONMENT);
        $before = $environment->getVersions();
        $manager->migrate(self::ENVIRONMENT);
        $after = $environment->getVersions();

        return new MigrationResult(array_values(array_diff($after, $before)));
    }
}
