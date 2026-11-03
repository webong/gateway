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

    private const REQUIRED_TABLES = [
        'accounts',
        'targets',
        'servers',
        'applications',
        'instances',
        'nodes',
        'events',
        'schedules',
        'schedule_runs',
        'schedule_notifications',
        'usage_counters',
        'deliveries',
        'gateway_cache',
        'gateway_cache_locks',
        'delivery_admin_endpoints',
        'configurations',
        'configuration_revisions',
        'resource_observations',
        'configuration_secrets',
        'resource_checkpoints',
        'companion_action_executions',
    ];

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
        return $this->run(fake: false);
    }

    public function adoptExistingSchema(): MigrationResult
    {
        $manager = $this->manager();
        $adapter = $manager->getEnvironment(self::ENVIRONMENT)->getAdapter();
        $missingTables = array_values(array_filter(
            self::REQUIRED_TABLES,
            static fn (string $table): bool => ! $adapter->hasTable($table),
        ));

        if ($missingTables !== []) {
            throw new \RuntimeException(sprintf(
                'Cannot adopt an incomplete Gateway schema; missing tables: %s.',
                implode(', ', $missingTables),
            ));
        }

        return $this->run(fake: true, manager: $manager);
    }

    private function run(bool $fake, ?Manager $manager = null): MigrationResult
    {
        $manager ??= $this->manager();
        $environment = $manager->getEnvironment(self::ENVIRONMENT);
        $before = $environment->getVersions();
        $manager->migrate(self::ENVIRONMENT, fake: $fake);
        $after = $environment->getVersions();

        return new MigrationResult(array_values(array_diff($after, $before)));
    }

    private function manager(): Manager
    {
        return new Manager(new Config([
            'paths' => ['migrations' => $this->migrationsPath],
            'environments' => [
                'default_environment' => self::ENVIRONMENT,
                self::ENVIRONMENT => [
                    ...$this->connection,
                    'migration_table' => 'gateway_migrations',
                ],
            ],
        ]), new ArrayInput([]), new NullOutput());
    }
}
