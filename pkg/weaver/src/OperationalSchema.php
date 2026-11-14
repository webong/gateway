<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver;

use PDO;
use RuntimeException;
use Throwable;

/** Explicit operational installation; activation never creates or adopts tables. */
final class OperationalSchema
{
    public const VERSION = 1;

    public static function install(PDO $connection, int $capacity = 10): void
    {
        if ($capacity < 1 || $capacity > 1024) {
            throw new \InvalidArgumentException('Queue capacity must be between 1 and 1024.');
        }
        $driver = self::driver($connection);
        if (self::hasTable($connection, 'gateway_workflow_schema')) {
            self::assertReady($connection);
            return;
        }
        foreach (['workflow_rpc_states', 'workflow_queue_lock', 'workflow_queue_runs', 'workflow_queue_claims', 'workflow_queue_outbox'] as $table) {
            if (self::hasTable($connection, $table)) {
                throw new RuntimeException('Existing WorkFlow data requires an explicit migration rehearsal; installation will not adopt or overwrite it.');
            }
        }
        // MySQL DDL commits implicitly. A failed partial install deliberately has
        // no version marker and requires operator reconciliation before retry.
        $transactional = $driver !== 'mysql';
        if ($transactional) { $connection->beginTransaction(); }
        try {
            $sql = file_get_contents(__DIR__.'/../schema/'.$driver.'.sql');
            if ($sql === false) { throw new RuntimeException('Operational schema is unavailable.'); }
            $connection->exec($sql);
            QueueScheduler::install($connection, $capacity);
            $connection->exec('CREATE TABLE gateway_workflow_schema (id INTEGER PRIMARY KEY, version INTEGER NOT NULL)');
            $connection->exec('INSERT INTO gateway_workflow_schema VALUES (1, '.self::VERSION.')');
            if ($transactional) { $connection->commit(); }
        } catch (Throwable $error) {
            if ($transactional && $connection->inTransaction()) { $connection->rollBack(); }
            throw $error;
        }
    }

    public static function assertReady(PDO $connection): void
    {
        self::driver($connection);
        try {
            $version = $connection->query('SELECT version FROM gateway_workflow_schema WHERE id = 1')->fetchColumn();
            if ((int) $version !== self::VERSION) {
                throw new RuntimeException('Unsupported operational schema version.');
            }
            $connection->query('SELECT subject_type, subject_id, flow_key, state, initialized, lock_version FROM workflow_rpc_states WHERE 1 = 0');
            $capacity = $connection->query('SELECT capacity FROM workflow_queue_lock WHERE id = 1')->fetchColumn();
            if ($capacity === false || (int) $capacity < 1) {
                throw new RuntimeException('Queue capacity record is missing.');
            }
            $connection->query('SELECT id, state, context FROM workflow_queue_runs WHERE 1 = 0');
            $connection->query('SELECT id, run_id, step_id, status, expires_at, payload, result FROM workflow_queue_claims WHERE 1 = 0');
            $connection->query('SELECT id, type, payload, status, token, available_at, expires_at FROM workflow_queue_outbox WHERE 1 = 0');
        } catch (Throwable $error) {
            throw new RuntimeException('WorkFlow operational storage is not ready; run the explicit migration command.', previous: $error);
        }
    }

    public static function driver(PDO $connection): string
    {
        return match ($connection->getAttribute(PDO::ATTR_DRIVER_NAME)) {
            'sqlite' => 'sqlite', 'pgsql' => 'postgres', 'mysql' => 'mysql',
            default => throw new \InvalidArgumentException('WorkFlow requires SQLite, PostgreSQL or MySQL.'),
        };
    }

    private static function hasTable(PDO $connection, string $table): bool
    {
        $statement = $connection->prepare(match (self::driver($connection)) {
            'sqlite' => "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
            'postgres' => 'SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = ?',
            'mysql' => 'SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?',
        });
        $statement->execute([$table]);
        return (int) $statement->fetchColumn() > 0;
    }
}
