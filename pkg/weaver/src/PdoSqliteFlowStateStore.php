<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver;

use Throwable;

/**
 * SQLite-backed state store without Illuminate dependencies.
 *
 * SQLite has no row locks, so mutate() opens the transaction with
 * BEGIN IMMEDIATE, taking the database-wide write lock before reading.
 * Concurrent writers therefore serialize instead of racing the
 * read-modify-write cycle. Set `PRAGMA busy_timeout` on the connection so
 * that contending writers wait rather than fail; see bootstrap.example.php.
 */
final readonly class PdoSqliteFlowStateStore extends PdoFlowStateStore
{
    protected function insertIgnoredSql(): string
    {
        return 'INSERT INTO workflow_rpc_states (subject_type, subject_id, flow_key, state, initialized)
                VALUES (:type, :id, :flow_key, :state, FALSE)
                ON CONFLICT (subject_type, subject_id, flow_key) DO NOTHING';
    }

    protected function lockSql(): string
    {
        return '';
    }

    protected function beginWriteTransaction(): void
    {
        // PDO cannot be asked to begin an IMMEDIATE transaction, so the
        // transaction is managed with raw statements instead of
        // beginTransaction()/commit()/rollBack().
        $this->connection->exec('BEGIN IMMEDIATE');
    }

    protected function commitWriteTransaction(): void
    {
        $this->connection->exec('COMMIT');
    }

    protected function rollbackWriteTransaction(): void
    {
        try {
            $this->connection->exec('ROLLBACK');
        } catch (Throwable) {
            // The transaction was already rolled back or never started.
        }
    }
}
