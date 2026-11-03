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

    private const REQUIRED_COLUMNS = [
        'accounts' => ['id', 'token_hash', 'created_at', 'updated_at'],
        'targets' => ['id', 'name', 'driver', 'connection', 'worker_token_hash'],
        'servers' => ['id', 'type', 'name', 'hostname', 'target_id'],
        'applications' => ['id', 'server_id', 'external_id', 'revision'],
        'instances' => ['id', 'server_id', 'node_id', 'state', 'revision'],
        'nodes' => ['id', 'drivers', 'session_id', 'heartbeat_at', 'target_id'],
        'events' => ['id', 'event_id', 'protocol', 'kind', 'received_at'],
        'schedules' => ['id', 'name', 'cron', 'language', 'source', 'payload'],
        'schedule_runs' => ['id', 'schedule_id', 'status', 'claim_token', 'lease_until'],
        'schedule_notifications' => ['run_id', 'state', 'attempts', 'next_attempt_at'],
        'usage_counters' => ['metric', 'subject_hash', 'day', 'attempts', 'bytes'],
        'deliveries' => ['id', 'payload', 'state', 'next_attempt_at', 'lease_token'],
        'gateway_cache' => ['key', 'value', 'expiration'],
        'gateway_cache_locks' => ['key', 'owner', 'expiration'],
        'delivery_admin_endpoints' => ['id', 'origin', 'expires_unix', 'signature'],
        'configurations' => ['id', 'kind', 'owner', 'key_hash', 'document', 'revision'],
        'configuration_revisions' => ['resource_id', 'kind', 'revision', 'operation', 'document'],
        'resource_observations' => ['resource_id', 'state', 'observed_revision', 'observed_at'],
        'configuration_secrets' => ['resource_id', 'ciphertext', 'version'],
        'resource_checkpoints' => ['resource_id', 'cursor', 'lease_token', 'lease_until'],
        'companion_action_executions' => ['execution_id', 'action', 'idempotency_hash', 'status'],
    ];

    /** @param array<string, mixed> $connection */
    private function __construct(
        private array $connection,
        private string $migrationsPath,
        private array $tables,
    ) {}

    /** @param array<string, string> $tables */
    public static function sqlite(string $database, string $migrationsPath, array $tables = []): self
    {
        return new self([
            'adapter' => 'sqlite',
            'name' => $database,
            'suffix' => '',
        ], $migrationsPath, $tables);
    }

    /** @param array<string, string> $tables */
    public static function postgres(
        string $host,
        int $port,
        string $database,
        string $username,
        string $password,
        string $migrationsPath,
        array $tables = [],
    ): self {
        return new self([
            'adapter' => 'pgsql',
            'host' => $host,
            'port' => $port,
            'name' => $database,
            'user' => $username,
            'pass' => $password,
            'charset' => 'utf8',
        ], $migrationsPath, $tables);
    }

    /**
     * @param array<string, mixed> $connection
     * @param array<string, string> $tables
     */
    public static function fromConnection(array $connection, string $migrationsPath, array $tables = []): self
    {
        $driver = $connection['driver'] ?? null;

        if ($driver === 'sqlite') {
            return self::sqlite((string) ($connection['database'] ?? ''), $migrationsPath, $tables);
        }

        if ($driver === 'pgsql') {
            return self::postgres(
                host: (string) ($connection['host'] ?? '127.0.0.1'),
                port: (int) ($connection['port'] ?? 5432),
                database: (string) ($connection['database'] ?? ''),
                username: (string) ($connection['username'] ?? ''),
                password: (string) ($connection['password'] ?? ''),
                migrationsPath: $migrationsPath,
                tables: $tables,
            );
        }

        if ($driver === 'mysql' || $driver === 'mariadb') {
            return new self([
                'adapter' => 'mysql',
                'host' => (string) ($connection['host'] ?? '127.0.0.1'),
                'port' => (int) ($connection['port'] ?? 3306),
                'name' => (string) ($connection['database'] ?? ''),
                'user' => (string) ($connection['username'] ?? ''),
                'pass' => (string) ($connection['password'] ?? ''),
                'charset' => (string) ($connection['charset'] ?? 'utf8mb4'),
            ], $migrationsPath, $tables);
        }

        throw new \InvalidArgumentException(sprintf(
            'Unsupported Gateway migration database driver [%s].',
            is_scalar($driver) ? (string) $driver : get_debug_type($driver),
        ));
    }

    public function migrate(): MigrationResult
    {
        return $this->run(fake: false);
    }

    public function adoptExistingSchema(): MigrationResult
    {
        $manager = $this->manager();
        $adapter = $manager->getEnvironment(self::ENVIRONMENT)->getAdapter();
        $missing = [];
        foreach (self::REQUIRED_COLUMNS as $logicalTable => $requiredColumns) {
            $table = $this->tables[$logicalTable] ?? $logicalTable;
            if (! $adapter->hasTable($table)) {
                $missing[] = $table;

                continue;
            }

            $columns = array_map(
                static fn ($column): string => $column->getName(),
                $adapter->getColumns($table),
            );
            foreach (array_diff($requiredColumns, $columns) as $column) {
                $missing[] = $table.'.'.$column;
            }
        }

        if ($missing !== []) {
            throw new \RuntimeException(sprintf(
                'Cannot adopt an incomplete Gateway schema; missing schema elements: %s.',
                implode(', ', $missing),
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
                    'gateway_tables' => $this->tables,
                ],
            ],
        ]), new ArrayInput([]), new NullOutput());
    }
}
