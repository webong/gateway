<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver;

use Webong\WorkFlow\Contracts\FlowStepExecutor;
use Webong\WorkFlow\Services\FlowStepExecutorResolver;
use Webong\WorkFlow\Services\FlowStepExecution;
use Webong\WorkFlow\ValueObjects\ArrayFlowContext;
use Webong\WorkFlow\ValueObjects\FlowStepInput;
use Webong\WorkFlow\ValueObjects\FlowStepInvocation;
use Webong\WorkFlow\ValueObjects\StepResult;
use Webong\WorkFlow\ValueObjects\StepState;

/** One synchronous executor per process; multiple processes provide bounded fan-out. */
final class QueueWorker
{
    /** @param list<FlowStepExecutor> $executors */
    public function __construct(
        private QueueScheduler $scheduler,
        private array $executors,
        private FlowStepExecution $execution = new FlowStepExecution(),
        private int $executionLeaseSeconds=0,
    ) {
    }

    /** Direct polling mode. The host waits until nextWakeup() or a notification between calls. */
    public function tick(int $leaseSeconds = 300): bool
    {
        $claims = $this->scheduler->claim(1, $leaseSeconds);
        if ($claims === []) {
            return false;
        }
        return $this->handle($claims[0]->claimId);
    }

    /** Broker mode. Only the opaque claim ID is accepted; payload and executors are host-owned. */
    public function handle(string $claimId): bool
    {
        $claim = $this->scheduler->beginExecution($claimId,$this->executionLeaseSeconds);
        if ($claim === null) {
            return false;
        }
        $executor = (new FlowStepExecutorResolver())->resolve($claim->step, $this->executors);
        if ($executor === null) {
            $this->scheduler->complete($claim, StepResult::failed('No registered executor supports this step.', metadata: ['error_code' => 'executor_unavailable']));
            return true;
        }
        $attempt = $claim->metadata['attempt'];
        assert(is_int($attempt));
        $invocation = new FlowStepInvocation(
            step: $claim->step,
            input: new FlowStepInput($claim->input),
            context: new ArrayFlowContext([...$claim->context, '_queue' => [
                'claim_id' => $claim->claimId,
                'idempotency_key' => $claim->metadata['idempotency_key'],
            ]]),
            previous: StepState::fromArray($claim->previousState),
            flowKey: $claim->run->definition->key,
            runId: $claim->run->id,
            attempt: $attempt,
        );
        try {
            $outcome = $this->execution->execute($executor, $invocation);
        } catch (\InvalidArgumentException) {
            // Schema violations gate external effects; do not retry invalid inputs.
            $this->scheduler->complete($claim, StepResult::failed('Step input violated its schema.', metadata: ['error_code' => 'input_schema_violation']));
            return true;
        }
        $this->scheduler->complete($claim, new StepResult(
            status: $outcome->status,
            message: $outcome->message,
            error: $outcome->error,
            retriable: $outcome->retriable,
            nextRetryAt: $outcome->nextRetryAt,
            metadata: $outcome->metadata,
            deferred: ($outcome->metadata['deferred'] ?? false) === true,
            output: $outcome->output,
            deadlineAt: $outcome->deadlineAt,
        ));
        return true;
    }
}
