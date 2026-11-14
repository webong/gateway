<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver;

use Closure;
use InvalidArgumentException;
use PDO;
use RuntimeException;
use Throwable;
use Webong\WorkFlow\Contracts\FlowQueueScheduler;
use Webong\WorkFlow\Enums\FlowStatus;
use Webong\WorkFlow\Enums\FlowStepStatus;
use Webong\WorkFlow\Services\CanonicalFlowPayload;
use Webong\WorkFlow\Services\FlowEvaluator;
use Webong\WorkFlow\Services\FlowInputResolver;
use Webong\WorkFlow\Services\FlowReadySet;
use Webong\WorkFlow\Services\FlowStateTransition;
use Webong\WorkFlow\Services\NullFlowEventSink;
use Webong\WorkFlow\ValueObjects\ArrayFlowContext;
use Webong\WorkFlow\ValueObjects\FlowDeferredCompletion;
use Webong\WorkFlow\ValueObjects\FlowRun;
use Webong\WorkFlow\ValueObjects\FlowState;
use Webong\WorkFlow\ValueObjects\FlowStepClaim;
use Webong\WorkFlow\ValueObjects\StepResult;
use Webong\WorkFlow\ValueObjects\StepState;

/** A deliberately serialized reference scheduler; effects never run under its database lock. */
final class QueueScheduler implements FlowQueueScheduler
{
    private ?int $transactionTime = null;
    /** @param null|Closure(): int $clock */
    public function __construct(private PDO $pdo, private ?Closure $clock = null)
    {
        $pdo->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);
        if ($pdo->getAttribute(PDO::ATTR_DRIVER_NAME) === 'sqlite') {
            $pdo->exec('PRAGMA busy_timeout = 10000');
        }
    }

    /** Run once during deployment, before starting workers. Separate tables from RPC projections. */
    public static function install(PDO $pdo, int $maxConcurrency = 10): void
    {
        if ($maxConcurrency < 1) {
            throw new InvalidArgumentException('Concurrency must be positive.');
        }
        $mysql = $pdo->getAttribute(PDO::ATTR_DRIVER_NAME) === 'mysql';
        foreach ([
            'CREATE TABLE workflow_queue_lock (id INTEGER PRIMARY KEY, capacity INTEGER NOT NULL, revision INTEGER NOT NULL)',
            'CREATE TABLE workflow_queue_runs (id VARCHAR(191) PRIMARY KEY, state TEXT NOT NULL, context TEXT NOT NULL)',
            'CREATE TABLE workflow_queue_claims (id VARCHAR(64) PRIMARY KEY, run_id VARCHAR(191) NOT NULL, step_id VARCHAR(191) NOT NULL, status VARCHAR(24) NOT NULL, expires_at BIGINT NOT NULL, payload TEXT NOT NULL, result TEXT)',
            'CREATE INDEX workflow_queue_claims_active ON workflow_queue_claims (status, expires_at)',
            'CREATE TABLE workflow_queue_outbox (id VARCHAR(64) PRIMARY KEY, type VARCHAR(64) NOT NULL, payload TEXT NOT NULL, status VARCHAR(24) NOT NULL, token VARCHAR(64), available_at BIGINT NOT NULL, expires_at BIGINT NOT NULL)',
            'CREATE INDEX workflow_queue_outbox_ready ON workflow_queue_outbox (status, available_at)',
        ] as $sql) {
            if ($mysql && str_starts_with($sql, 'CREATE TABLE')) {
                $sql = str_replace(' TEXT', ' LONGTEXT', $sql) . ' ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin';
            }
            $pdo->exec($sql);
        }
        $pdo->prepare('INSERT INTO workflow_queue_lock VALUES (1, ?, 0)')->execute([$maxConcurrency]);
    }

    /** @param array<string, mixed> $context */
    public function start(FlowRun $run, array $context = [], ?string $storageId = null): FlowState
    {
        foreach (array_keys($context) as $key) {
            if (! is_string($key)) {
                throw new InvalidArgumentException('Queue context must use named keys.');
            }
        }
        $storageId ??= $run->id;
        if ($storageId === '' || strlen($storageId) > 191) {
            throw new InvalidArgumentException('Invalid queue storage ID.');
        }
        return $this->transaction(function () use ($run, $context, $storageId): FlowState {
            $existing = $this->get($storageId);
            if ($existing !== null) {
                if ($existing->run?->id !== $run->id) {
                    throw new InvalidArgumentException('Queue storage ID belongs to another logical run.');
                }
                $existing->run->assertDefinition($run->definition);
                $row = $this->row('SELECT context FROM workflow_queue_runs WHERE id = ?', [$storageId]);
                assert($row !== null);
                if (CanonicalFlowPayload::fingerprint($context) !== CanonicalFlowPayload::fingerprint($this->decode((string) $row['context']))) {
                    throw new InvalidArgumentException('A run ID cannot be reused with different context.');
                }
                return $existing;
            }
            if (strlen($run->id) > 191) {
                throw new InvalidArgumentException('Run ID is too long.');
            }
            foreach ($run->definition->steps as $step) {
                if (strlen($step->id) > 191) {
                    throw new InvalidArgumentException('Step ID is too long.');
                }
            }
            $state = (new FlowEvaluator())->evaluate($run->definition, $run->initialState()->withMetadata(['_queue_storage_id' => $storageId]));
            $this->pdo->prepare('INSERT INTO workflow_queue_runs VALUES (?, ?, ?)')->execute([
                $storageId, $this->encode($state->toArray()), $this->encode($context),
            ]);
            $this->event('run.started', ['run_id' => $run->id, 'storage_id' => $storageId]);
            return $state;
        });
    }

    public function get(string $runId): ?FlowState
    {
        $row = $this->row('SELECT state FROM workflow_queue_runs WHERE id = ?', [$runId]);
        return $row === null ? null : FlowState::fromArray($this->decode((string) $row['state']));
    }

    public function claim(int $maxClaims, int $leaseSeconds): array
    {
        $this->positive($leaseSeconds);
        if ($maxClaims < 1) {
            return [];
        }
        return $this->transaction(function () use ($maxClaims, $leaseSeconds): array {
            $this->recover();
            $capacity = $this->row('SELECT capacity FROM workflow_queue_lock WHERE id = 1');
            $count = $this->row("SELECT COUNT(*) AS n FROM workflow_queue_claims WHERE status IN ('claimed', 'executing')");
            assert($capacity !== null && $count !== null);
            $remaining = min($maxClaims, (int) $capacity['capacity'] - (int) $count['n']);
            $claims = [];
            $runs = $this->pdo->query('SELECT id, state, context FROM workflow_queue_runs ORDER BY id');
            assert($runs !== false);
            foreach ($runs->fetchAll(PDO::FETCH_ASSOC) as $row) {
                $state = FlowState::fromArray($this->decode((string) $row['state']));
                $run = $state->run;
                assert($run !== null);
                $context = $this->decode((string) $row['context']);
                foreach ((new FlowReadySet())->steps($run->definition, $state, $this->now()) as $step) {
                    if ($remaining <= 0) {
                        break 2;
                    }
                    $previous = $state->steps[$step->id] ?? new StepState();
                    if (($previous->metadata['queue_paused'] ?? false) === true) {
                        continue;
                    }
                    try {
                        $input = (new FlowInputResolver())->resolve(new NullFlowEventSink(), $run->definition->key, $step, $state, new ArrayFlowContext($context))->all();
                    } catch (InvalidArgumentException) {
                        $state = $state->withStep($step->id, new StepState(FlowStepStatus::FAILED, error: 'Step input could not be resolved.', retriable: false, attempts: $previous->attempts, metadata: ['error_code' => 'input_resolution_failed']));
                        $this->save($state);
                        $this->event('step.failed', ['run_id' => $run->id, 'storage_id' => $this->storageId($state), 'step_id' => $step->id]);
                        continue;
                    }
                    $id = bin2hex(random_bytes(16));
                    $claim = new FlowStepClaim($id, $run, $step, $state, $input, $context, $previous->toArray(), [
                        'storage_id' => (string) $row['id'],
                        'attempt' => $previous->attempts + 1,
                        'lease_expires_at' => $this->now() + $leaseSeconds,
                        // Stable across attempts: the host must deduplicate external effects on this key.
                        'idempotency_key' => hash('sha256', $this->encode([(string) $row['id'], $step->id])),
                    ]);
                    $this->pdo->prepare('INSERT INTO workflow_queue_claims VALUES (?, ?, ?, ?, ?, ?, NULL)')->execute([
                        $id, (string) $row['id'], $step->id, 'claimed', $this->now() + $leaseSeconds, $this->encode($claim->toArray()),
                    ]);
                    $state = $state->withStep($step->id, new StepState(FlowStepStatus::RUNNING, attempts: $previous->attempts + 1));
                    $this->save($state);
                    $this->event('work.ready', ['claim_id' => $id]);
                    $claims[] = $claim;
                    --$remaining;
                }
            }
            return $claims;
        });
    }

    /** Atomic delivery gate. Duplicate queue deliveries never execute a live attempt twice. */
    public function beginExecution(string $claimId): ?FlowStepClaim
    {
        return $this->transaction(function () use ($claimId): ?FlowStepClaim {
            $row = $this->row('SELECT * FROM workflow_queue_claims WHERE id = ?', [$claimId]);
            if ($row === null || $row['status'] !== 'claimed' || (int) $row['expires_at'] <= $this->now()) {
                return null;
            }
            $claim = FlowStepClaim::fromArray($this->decode((string) $row['payload']));
            $state = $this->get($this->claimStorageId($claim));
            if ($state === null || $state->status === FlowStatus::CANCELLED) {
                return null;
            }
            $this->pdo->prepare("UPDATE workflow_queue_claims SET status = 'executing' WHERE id = ?")->execute([$claimId]);
            return $claim;
        });
    }

    public function renew(FlowStepClaim|string $claim, int $leaseSeconds): void
    {
        $this->positive($leaseSeconds);
        $this->transaction(function () use ($claim, $leaseSeconds): void {
            if (is_string($claim)) {
                $row = $this->row('SELECT payload FROM workflow_queue_claims WHERE id = ?', [$claim]);
                if ($row === null) {
                    throw new RuntimeException('Step claim lease lost.');
                }
                $claim = FlowStepClaim::fromArray($this->decode((string) $row['payload']));
            }
            $this->owned($claim);
            $this->pdo->prepare('UPDATE workflow_queue_claims SET expires_at = ? WHERE id = ?')->execute([$this->now() + $leaseSeconds, $claim->claimId]);
        });
    }

    public function complete(FlowStepClaim $claim, StepResult $result): void
    {
        if ($result->status === FlowStepStatus::RUNNING) {
            throw new InvalidArgumentException('An executor must return a final or pending outcome.');
        }
        $this->transaction(function () use ($claim, $result): void {
            $row = $this->row('SELECT * FROM workflow_queue_claims WHERE id = ?', [$claim->claimId]);
            $fingerprint = CanonicalFlowPayload::fingerprint($result->toArray());
            if ($row !== null && $row['status'] === 'finished' && $row['result'] === $fingerprint) {
                return;
            }
            $actual = $this->owned($claim);
            $this->finish($actual, $result, 'finished', $fingerprint);
        });
    }

    /** Releasing a possibly executed claim follows the same bounded, idempotent retry policy as lease loss. */
    public function release(FlowStepClaim $claim): void
    {
        $this->transaction(function () use ($claim): void {
            $actual = $this->owned($claim);
            $this->finish($actual, $this->lostResult($actual), 'released');
        });
    }

    public function fail(FlowStepClaim $claim, Throwable $exception): void
    {
        // Exception details belong in the host's private failure reporter, never persisted in public state.
        $this->complete($claim, new StepResult(FlowStepStatus::FAILED, error: 'Flow step failed.', metadata: ['error_code' => 'step_execution_failed']));
    }

    public function cancelRun(string $runId): void
    {
        $this->transaction(function () use ($runId): void {
            $state = $this->required($runId);
            if ($state->status === FlowStatus::CANCELLED) {
                return;
            }
            $this->save((new FlowStateTransition())->cancel($state));
            $this->pdo->prepare("UPDATE workflow_queue_claims SET status = 'cancelled' WHERE run_id = ? AND status IN ('claimed', 'executing')")->execute([$runId]);
        });
    }

    /** Explicitly start a new pass for plain pending results; deferred waits still require callbacks. */
    public function resumeRun(string $runId): void
    {
        $this->transaction(function () use ($runId): void {
            $state = $this->required($runId);
            if (in_array($state->status, [FlowStatus::CANCELLED, FlowStatus::COMPLETED], true)) {
                throw new InvalidArgumentException('A terminal queue run cannot resume.');
            }
            $changed = false;
            foreach ($state->steps as $id => $step) {
                if (($step->metadata['queue_paused'] ?? false) === true) {
                    $changed = true;
                    $data = $step->toArray();
                    $metadata = $step->metadata;
                    unset($metadata['queue_paused']);
                    $data['metadata'] = $metadata;
                    $state = $state->withStep($id, StepState::fromArray($data));
                }
            }
            if (! $changed) {
                return;
            }
            $this->save($state);
            $this->event('run.resumed', ['run_id' => $state->run?->id, 'storage_id' => $runId]);
        });
    }

    public function completeDeferred(FlowDeferredCompletion $completion, ?string $storageId = null): FlowState
    {
        return $this->transaction(function () use ($completion, $storageId): FlowState {
            if ($completion->runId === null) {
                throw new InvalidArgumentException('Queue callbacks require a run ID.');
            }
            $state = $this->required($storageId ?? $completion->runId);
            $step = $state->run?->definition->step($completion->stepId);
            if ($completion->result->status === FlowStepStatus::FAILED && $step?->retryPolicy === null) {
                $result = $completion->result;
                $completion = new FlowDeferredCompletion($completion->flowKey, $completion->stepId, $completion->idempotencyKey,
                    new StepResult($result->status, $result->message, $result->error, false, metadata: $result->metadata, output: $result->output),
                    $completion->runId, $completion->attempt);
            }
            $next = (new FlowStateTransition(clock: fn (): int => $this->now()))->completeDeferred($state, $completion);
            if ($next !== $state) {
                $this->save($next);
                $this->event('step.' . $completion->result->status->value, ['run_id' => $completion->runId, 'storage_id' => $storageId ?? $completion->runId, 'step_id' => $completion->stepId, 'attempt' => $completion->attempt]);
            }
            return $next;
        });
    }

    public function reconcile(): int
    {
        return $this->transaction(fn (): int => $this->recover());
    }

    /** Next persisted retry, lease or wait deadline; null means wait for external work. */
    public function nextWakeup(): ?int
    {
        $times = [];
        $row = $this->row("SELECT MIN(expires_at) AS t FROM workflow_queue_claims WHERE status IN ('claimed', 'executing')");
        if ($row !== null && $row['t'] !== null) {
            $times[] = (int) $row['t'];
        }
        $rows = $this->pdo->query('SELECT state FROM workflow_queue_runs');
        assert($rows !== false);
        foreach ($rows->fetchAll(PDO::FETCH_ASSOC) as $row) {
            $state = FlowState::fromArray($this->decode((string) $row['state']));
            if (in_array($state->status, [FlowStatus::CANCELLED, FlowStatus::COMPLETED], true)) {
                continue;
            }
            $run = $state->run;
            assert($run !== null);
            foreach ((new FlowReadySet())->steps($run->definition, $state, $this->now()) as $ready) {
                if (($state->steps[$ready->id]->metadata['queue_paused'] ?? false) !== true) {
                    $times[] = $this->now();
                    break;
                }
            }
            foreach ($state->steps as $step) {
                if ($step->nextRetryAt !== null) {
                    $times[] = $step->nextRetryAt;
                }
                if ($step->deadlineAt !== null) {
                    $times[] = $step->deadlineAt;
                }
            }
        }
        return $times === [] ? null : min($times);
    }

    /** @return list<array{id: string, type: string, payload: array<string, mixed>, token: string}> */
    public function reserveEvents(int $limit = 100, int $leaseSeconds = 30): array
    {
        $this->positive($leaseSeconds);
        if ($limit < 1) {
            return [];
        }
        return $this->transaction(function () use ($limit, $leaseSeconds): array {
            $stmt = $this->pdo->prepare("SELECT * FROM workflow_queue_outbox WHERE (status = 'pending' AND available_at <= ?) OR (status = 'delivering' AND expires_at <= ?) ORDER BY available_at, id LIMIT " . (int) $limit);
            $stmt->execute([$this->now(), $this->now()]);
            $events = [];
            foreach ($stmt->fetchAll(PDO::FETCH_ASSOC) as $row) {
                $token = bin2hex(random_bytes(16));
                $this->pdo->prepare("UPDATE workflow_queue_outbox SET status = 'delivering', token = ?, expires_at = ? WHERE id = ?")->execute([$token, $this->now() + $leaseSeconds, $row['id']]);
                $events[] = ['id' => (string) $row['id'], 'type' => (string) $row['type'], 'payload' => $this->decode((string) $row['payload']), 'token' => $token];
            }
            return $events;
        });
    }

    public function acknowledgeEvent(string $id, string $token): void
    {
        $this->eventOutcome($id, $token, true, 0);
    }

    public function retryEvent(string $id, string $token, int $delaySeconds = 5): void
    {
        $this->positive($delaySeconds);
        $this->eventOutcome($id, $token, false, $delaySeconds);
    }

    private function eventOutcome(string $id, string $token, bool $done, int $delay): void
    {
        $this->transaction(function () use ($id, $token, $done, $delay): void {
            $stmt = $this->pdo->prepare("UPDATE workflow_queue_outbox SET status = ?, token = NULL, available_at = ? WHERE id = ? AND status = 'delivering' AND token = ? AND expires_at > ?");
            $stmt->execute([$done ? 'delivered' : 'pending', $this->now() + $delay, $id, $token, $this->now()]);
            if ($stmt->rowCount() !== 1) {
                throw new RuntimeException('Outbox delivery lease lost.');
            }
        });
    }

    private function recover(): int
    {
        $stmt = $this->pdo->prepare("SELECT payload FROM workflow_queue_claims WHERE status IN ('claimed', 'executing') AND expires_at <= ?");
        $stmt->execute([$this->now()]);
        $expired = $stmt->fetchAll(PDO::FETCH_ASSOC);
        foreach ($expired as $row) {
            $claim = FlowStepClaim::fromArray($this->decode((string) $row['payload']));
            $this->finish($claim, $this->lostResult($claim), 'expired');
        }
        $rows = $this->pdo->query('SELECT state FROM workflow_queue_runs');
        assert($rows !== false);
        foreach ($rows->fetchAll(PDO::FETCH_ASSOC) as $row) {
            $state = FlowState::fromArray($this->decode((string) $row['state']));
            if (in_array($state->status, [FlowStatus::CANCELLED, FlowStatus::COMPLETED], true)) {
                continue;
            }
            foreach ($state->steps as $id => $step) {
                if (($step->metadata['deferred'] ?? false) === true && $step->deadlineAt !== null && $step->deadlineAt <= $this->now()) {
                    $run = $state->run;
                    assert($run !== null);
                    $state = (new FlowStateTransition(clock: fn (): int => $this->now()))->deadlineReached($run->definition, $state, $id);
                    $this->save($state);
                    $this->event('step.deadline', ['run_id' => $run->id, 'storage_id' => $this->storageId($state), 'step_id' => $id]);
                }
            }
        }
        return count($expired);
    }

    private function lostResult(FlowStepClaim $claim): StepResult
    {
        return StepResult::failed('Execution outcome is unknown; reconcile external effects before replay.', retriable: $claim->step->retryPolicy?->idempotent === true, metadata: ['error_code' => 'claim_lost']);
    }

    private function owned(FlowStepClaim $claim): FlowStepClaim
    {
        $row = $this->row('SELECT * FROM workflow_queue_claims WHERE id = ?', [$claim->claimId]);
        if ($row === null || ! in_array($row['status'], ['claimed', 'executing'], true) || (int) $row['expires_at'] <= $this->now()) {
            throw new RuntimeException('Step claim lease lost.');
        }
        $actual = FlowStepClaim::fromArray($this->decode((string) $row['payload']));
        if ($actual->run->id !== $claim->run->id || $actual->step->id !== $claim->step->id || $actual->metadata['attempt'] !== ($claim->metadata['attempt'] ?? null) || $this->claimStorageId($actual) !== $this->claimStorageId($claim)) {
            throw new RuntimeException('Step claim identity mismatch.');
        }
        return $actual;
    }

    private function finish(FlowStepClaim $claim, StepResult $result, string $status, ?string $fingerprint = null): void
    {
        $state = $this->required($this->claimStorageId($claim));
        $current = $state->steps[$claim->step->id];
        if ($state->status === FlowStatus::CANCELLED || $current->status !== FlowStepStatus::RUNNING || $current->attempts !== $claim->metadata['attempt']) {
            throw new RuntimeException('Step attempt no longer owns this run.');
        }
        $retriable = $result->retriable;
        $retryAt = $result->nextRetryAt;
        if ($result->status === FlowStepStatus::FAILED) {
            // Queue retries require a bounded policy and explicit idempotency declaration.
            $retriable = ($retriable ?? true) && ($claim->step->retryPolicy?->canRetry($current->attempts) ?? false);
            $retryAt = $retriable ? max($retryAt ?? 0, $this->now() + $claim->step->retryPolicy->backoffSeconds) : null;
        }
        $metadata = $result->metadata;
        if ($result->deferred) {
            $metadata['deferred'] = true;
        } elseif ($result->status === FlowStepStatus::PENDING) {
            $metadata['queue_paused'] = true;
        }
        $next = new StepState($result->status, $result->message, $result->error, date(DATE_ATOM, $this->now()), $retriable, $current->attempts, $retryAt, $metadata, $result->output, $result->deferred ? $result->deadlineAt : null);
        $this->pdo->prepare('UPDATE workflow_queue_claims SET status = ?, result = ? WHERE id = ?')->execute([$status, $fingerprint, $claim->claimId]);
        $this->save($state->withStep($claim->step->id, $next));
        $this->event('step.' . $result->status->value, ['run_id' => $claim->run->id, 'storage_id' => $this->claimStorageId($claim), 'step_id' => $claim->step->id, 'attempt' => $current->attempts]);
    }

    private function save(FlowState $state): void
    {
        $run = $state->run;
        assert($run !== null);
        $storageId = $this->storageId($state);
        $old = $this->required($storageId);
        $next = (new FlowEvaluator())->evaluate($run->definition, $state);
        $this->pdo->prepare('UPDATE workflow_queue_runs SET state = ? WHERE id = ?')->execute([$this->encode($next->toArray()), $storageId]);
        if ($old->status !== $next->status && in_array($next->status, [FlowStatus::COMPLETED, FlowStatus::CANCELLED, FlowStatus::BLOCKED], true)) {
            $this->event('run.' . $next->status->value, ['run_id' => $run->id, 'storage_id' => $storageId]);
        }
    }

    /** @param array<string, mixed> $payload */
    private function event(string $type, array $payload): void
    {
        $this->pdo->prepare("INSERT INTO workflow_queue_outbox VALUES (?, ?, ?, 'pending', NULL, ?, 0)")->execute([bin2hex(random_bytes(16)), $type, $this->encode($payload), $this->now()]);
    }

    private function storageId(FlowState $state): string
    {
        $id = $state->metadata['_queue_storage_id'] ?? $state->run?->id;
        if (! is_string($id) || $id === '') {
            throw new RuntimeException('Queue snapshot has no storage identity.');
        }
        return $id;
    }

    private function claimStorageId(FlowStepClaim $claim): string
    {
        $id = $claim->metadata['storage_id'] ?? $claim->run->id;
        if (! is_string($id) || $id === '') {
            throw new RuntimeException('Queue claim has no storage identity.');
        }
        return $id;
    }

    private function required(string $runId): FlowState
    {
        return $this->get($runId) ?? throw new RuntimeException('Queue run not found.');
    }

    /**
     * @param list<mixed> $params
     * @return array<string, string|int|null>|null
     */
    private function row(string $sql, array $params = []): ?array
    {
        $stmt = $this->pdo->prepare($sql);
        $stmt->execute($params);
        /** @var array<string, string|int|null>|false $row */
        $row = $stmt->fetch(PDO::FETCH_ASSOC);
        return $row === false ? null : $row;
    }

    /**
     * @template T
     * @param Closure(): T $operation
     * @return T
     */
    private function transaction(Closure $operation): mixed
    {
        if ($this->pdo->inTransaction()) {
            throw new RuntimeException('Queue runtime requires its own transaction boundary.');
        }
        $this->pdo->beginTransaction();
        try {
            // First statement obtains the write lock on SQLite and a row lock on PostgreSQL/MySQL.
            $this->pdo->exec('UPDATE workflow_queue_lock SET revision = revision + 1 WHERE id = 1');
            $this->transactionTime = $this->databaseTime();
            $result = $operation();
            $this->pdo->commit();
            return $result;
        } catch (Throwable $e) {
            if ($this->pdo->inTransaction()) {
                $this->pdo->rollBack();
            }
            throw $e;
        } finally {
            $this->transactionTime = null;
        }
    }

    private function now(): int
    {
        return $this->clock !== null ? ($this->clock)() : ($this->transactionTime ?? $this->databaseTime());
    }

    private function databaseTime(): int
    {
        $expression = match ($this->pdo->getAttribute(PDO::ATTR_DRIVER_NAME)) {
            'sqlite' => "CAST(strftime('%s', 'now') AS INTEGER)",
            'pgsql' => 'FLOOR(EXTRACT(EPOCH FROM clock_timestamp()))',
            'mysql' => 'UNIX_TIMESTAMP()',
            default => throw new InvalidArgumentException('Unsupported queue database driver.'),
        };
        $row = $this->row('SELECT ' . $expression . ' AS epoch');
        assert($row !== null);
        return (int) $row['epoch'];
    }
    private function positive(int $value): void
    {
        if ($value < 1) { throw new InvalidArgumentException('Duration must be positive.'); }
    }
    private function encode(mixed $value): string
    {
        return json_encode(CanonicalFlowPayload::normalize($value), JSON_THROW_ON_ERROR);
    }
    /** @return array<string, mixed> */
    private function decode(string $value): array
    {
        $data = json_decode($value, true, flags: JSON_THROW_ON_ERROR);
        if (! is_array($data)) { throw new RuntimeException('Invalid queue snapshot.'); }
        foreach (array_keys($data) as $key) {
            if (! is_string($key)) { throw new RuntimeException('Queue snapshot must have string keys.'); }
        }
        /** @var array<string, mixed> $data */
        return $data;
    }
}
