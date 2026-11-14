<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver;

use Closure;
use JsonException;
use PDO;
use RuntimeException;
use Throwable;
use Webong\WorkFlow\Contracts\FlowStateSerializer;
use Webong\WorkFlow\Contracts\ForgettableFlowStateStore;
use Webong\WorkFlow\Enums\FlowStatus;
use Webong\WorkFlow\ValueObjects\FlowState;
use Webong\WorkFlow\ValueObjects\FlowStateSubject;

/**
 * Shared engine for the PDO-backed state stores.
 *
 * Every driver follows the same read-modify-write cycle inside a write
 * transaction: seed a PENDING row if it is missing, read the current state
 * under the driver's locking strategy, apply the transition closure, and
 * persist the result. The dialect seams are the insert statement, the row
 * lock fragment, and how the write transaction is opened and closed.
 */
abstract readonly class PdoFlowStateStore implements ForgettableFlowStateStore
{
    public function __construct(
        protected PDO $connection,
        private FlowStateSerializer $serializer,
        private FlowStateSubject $subject,
    ) {
    }

    public function get(string $flowKey): ?FlowState
    {
        $statement = $this->connection->prepare(
            'SELECT state, initialized FROM workflow_rpc_states WHERE subject_type = :type AND subject_id = :id AND flow_key = :flow_key',
        );
        $statement->execute($this->parameters($flowKey));
        $record = $statement->fetch(PDO::FETCH_ASSOC);

        if (! is_array($record) || ! $this->initialized($record['initialized'])) {
            return null;
        }

        return $this->decode($record['state']);
    }

    public function put(string $flowKey, FlowState $state): void
    {
        $this->mutate($flowKey, static fn (?FlowState $current): FlowState => $state);
    }

    public function mutate(string $flowKey, Closure $transition): FlowState
    {
        $this->beginWriteTransaction();

        try {
            $insert = $this->connection->prepare($this->insertIgnoredSql());
            $insert->execute([
                ...$this->parameters($flowKey),
                'state' => json_encode((new FlowState(FlowStatus::PENDING))->toArray(), JSON_THROW_ON_ERROR),
            ]);

            $select = $this->connection->prepare(
                'SELECT state, initialized FROM workflow_rpc_states
                 WHERE subject_type = :type AND subject_id = :id AND flow_key = :flow_key'.$this->lockSql(),
            );
            $select->execute($this->parameters($flowKey));
            $record = $select->fetch(PDO::FETCH_ASSOC);
            if (! is_array($record)) {
                throw new RuntimeException('Unable to lock the workflow RPC state');
            }

            $current = $this->initialized($record['initialized']) ? $this->decode($record['state']) : null;
            $next = $transition($current);

            $update = $this->connection->prepare(
                'UPDATE workflow_rpc_states
                 SET state = :state, initialized = TRUE, lock_version = lock_version + 1, updated_at = CURRENT_TIMESTAMP
                 WHERE subject_type = :type AND subject_id = :id AND flow_key = :flow_key',
            );
            $update->execute([
                ...$this->parameters($flowKey),
                'state' => json_encode($this->serializer->serialize($next), JSON_THROW_ON_ERROR),
            ]);
            $this->commitWriteTransaction();

            return $next;
        } catch (Throwable $exception) {
            $this->rollbackWriteTransaction();

            throw $exception;
        }
    }

    public function forget(string $flowKey): void
    {
        $statement = $this->connection->prepare(
            'DELETE FROM workflow_rpc_states WHERE subject_type = :type AND subject_id = :id AND flow_key = :flow_key',
        );
        $statement->execute($this->parameters($flowKey));
    }

    /** Full SQL that seeds the initial PENDING row, ignoring any existing row. */
    abstract protected function insertIgnoredSql(): string;

    /** SQL fragment appended to the mutating SELECT to lock the row, or an empty string. */
    abstract protected function lockSql(): string;

    abstract protected function beginWriteTransaction(): void;

    abstract protected function commitWriteTransaction(): void;

    abstract protected function rollbackWriteTransaction(): void;

    /** @return array{type: string, id: string, flow_key: string} */
    private function parameters(string $flowKey): array
    {
        return ['type' => $this->subject->type, 'id' => $this->subject->id, 'flow_key' => $flowKey];
    }

    private function decode(mixed $value): FlowState
    {
        if (! is_string($value)) {
            throw new RuntimeException('Invalid workflow RPC state payload');
        }

        try {
            $decoded = json_decode($value, true, 512, JSON_THROW_ON_ERROR);
        } catch (JsonException $exception) {
            throw new RuntimeException('Invalid workflow RPC state payload', previous: $exception);
        }

        if (! is_array($decoded)) {
            throw new RuntimeException('Invalid workflow RPC state payload');
        }

        foreach ($decoded as $key => $_) {
            if (! is_string($key)) {
                throw new RuntimeException('Invalid workflow RPC state payload');
            }
        }

        /** @var array<string, mixed> $decoded */
        return $this->serializer->deserialize($decoded);
    }

    private function initialized(mixed $value): bool
    {
        return in_array($value, [true, 1, '1', 't', 'true'], true);
    }
}
