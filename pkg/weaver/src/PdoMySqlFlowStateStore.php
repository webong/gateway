<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver;

/** MySQL (InnoDB)-backed state store without Illuminate dependencies. */
final readonly class PdoMySqlFlowStateStore extends PdoFlowStateStore
{
    protected function insertIgnoredSql(): string
    {
        return 'INSERT IGNORE INTO workflow_rpc_states (subject_type, subject_id, flow_key, state, initialized)
                VALUES (:type, :id, :flow_key, :state, FALSE)';
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
