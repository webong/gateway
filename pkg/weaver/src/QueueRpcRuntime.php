<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver;

use Webong\WorkFlow\Enums\FlowStatus;
use Webong\WorkFlow\ValueObjects\FlowDeferredCompletion;
use Webong\WorkFlow\ValueObjects\FlowState;
use Webong\WorkFlow\ValueObjects\FlowStateSubject;

/** The queue authority behind RPC. Routing reservations are never progress projections. */
final readonly class QueueRpcRuntime
{
    public function __construct(private QueueScheduler $scheduler) {}

    public function start(FlowStateSubject $subject, FlowState $reservation): FlowState
    {
        $run = $reservation->run ?? throw new RpcFailure(-32004, 'Flow run not found');
        $context = $reservation->metadata['_rpc_context'] ?? [];
        if (! is_array($context)) {
            throw new \RuntimeException('Invalid queue routing context.');
        }
        /** @var array<string, mixed> $context */
        return $this->decorate($subject, $this->scheduler->start($run, $context, $this->key($subject, $reservation)));
    }

    public function get(FlowStateSubject $subject, FlowState $reservation): FlowState
    {
        $state = $this->scheduler->get($this->key($subject, $reservation));
        if ($state === null) {
            throw new RpcFailure(-32009, 'Queue initialization is incomplete; retry flow.start');
        }
        return $this->decorate($subject, $state);
    }

    public function resume(FlowStateSubject $subject, FlowState $reservation): FlowState
    {
        $state = $this->start($subject, $reservation);
        if (! in_array($state->status, [FlowStatus::COMPLETED, FlowStatus::CANCELLED], true)) {
            $this->scheduler->resumeRun($this->key($subject, $reservation));
        }
        return $this->get($subject, $reservation);
    }

    public function complete(FlowStateSubject $subject, FlowState $reservation, FlowDeferredCompletion $completion): FlowState
    {
        // Read first so incomplete initialization never falls through to a routing snapshot.
        $this->get($subject, $reservation);
        return $this->decorate($subject, $this->scheduler->completeDeferred($completion, $this->key($subject, $reservation)));
    }

    public function cancel(FlowStateSubject $subject, FlowState $reservation): FlowState
    {
        $this->start($subject, $reservation);
        $this->scheduler->cancelRun($this->key($subject, $reservation));
        return $this->get($subject, $reservation);
    }

    private function key(FlowStateSubject $subject, FlowState $state): string
    {
        $run = $state->run ?? throw new RpcFailure(-32004, 'Flow run not found');
        $key = RpcRunIdentity::executionId($subject, $run->definition->key, $run->id);
        if (($state->metadata['_rpc_authority'] ?? null) === 'queue-v1'
            && ($state->metadata['execution_id'] ?? null) !== $key) {
            throw new RpcFailure(-32009, 'Queue routing scope does not match this request');
        }
        return $key;
    }

    private function decorate(FlowStateSubject $subject, FlowState $state): FlowState
    {
        return $state->withMetadata([
            'execution_driver' => 'queue',
            'execution_status' => 'dispatched',
            'execution_id' => $this->key($subject, $state),
        ]);
    }
}
