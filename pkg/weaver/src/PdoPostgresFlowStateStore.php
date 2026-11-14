<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver;

use Throwable;

/** PostgreSQL-backed state store without Illuminate dependencies. */
final readonly class PdoPostgresFlowStateStore extends PdoFlowStateStore
{
    protected function insertIgnoredSql(): string
    {
        return 'INSERT INTO workflow_rpc_states (subject_type, subject_id, flow_key, state, initialized)
                VALUES (:type, :id, :flow_key, :state, FALSE)
                ON CONFLICT (subject_type, subject_id, flow_key) DO NOTHING';
    }

    protected function lockSql(): string
    {
        return ' FOR UPDATE';
    }

    protected function beginWriteTransaction(): void
    {
        $this->connection->beginTransaction();
    }

    protected function commitWriteTransaction(): void
    {
        $this->connection->commit();
    }

    protected function rollbackWriteTransaction(): void
    {
        if ($this->connection->inTransaction()) {
            $this->connection->rollBack();
        }
    }
}
