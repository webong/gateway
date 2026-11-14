<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver;

use Webong\WorkFlow\Contracts\FlowDefinitionProvider;
use Webong\WorkFlow\Contracts\FlowStateStoreFactory;
use Webong\WorkFlow\OpenWorkflow\OpenWorkflow;
use Webong\WorkFlow\Enums\FlowStatus;
use Webong\WorkFlow\Enums\FlowStepStatus;
use Webong\WorkFlow\Services\FlowDefinitionValidator;
use Webong\WorkFlow\Services\FlowExecutionDispatcher;
use Webong\WorkFlow\Services\FlowOperationCatalog;
use Webong\WorkFlow\Services\FlowStateTransition;
use Webong\WorkFlow\Services\RunScopedFlowStateStore;
use Webong\WorkFlow\ValueObjects\FlowDefinition;
use Webong\WorkFlow\ValueObjects\FlowDeferredCompletion;
use Webong\WorkFlow\ValueObjects\FlowExecutionRequest;
use Webong\WorkFlow\ValueObjects\FlowOperation;
use Webong\WorkFlow\ValueObjects\FlowRun;
use Webong\WorkFlow\ValueObjects\FlowState;
use Webong\WorkFlow\ValueObjects\FlowStateSubject;
use Webong\WorkFlow\ValueObjects\StepOutput;
use Webong\WorkFlow\ValueObjects\StepResult;

/**
 * Trusted RPC facade. The host owns definitions, executors, and subject access.
 */
final readonly class FlowRpcApplication
{
    /** @var list<mixed> */
    private array $operationProviders;

    /**
     * @param iterable<mixed> $operationProviders Executors able to describe
     *     their operations; binding any enables the `flow.operations` method.
     * @param list<string>|null $grantedCapabilities Capabilities this host
     *     grants. RPC callers cannot override this trust boundary.
     */
    public function __construct(
        private FlowDefinitionProvider $definitions,
        private FlowStateStoreFactory $stores,
        private FlowExecutionDispatcher $execution,
        private FlowStateTransition $transitions = new FlowStateTransition(),
        private ?OpenWorkflow $ows = null,
        iterable $operationProviders = [],
        private ?array $grantedCapabilities = null,
        private ?QueueRpcRuntime $queue = null,
    ) {
        if ($this->queue !== null && $this->execution->has('queue')) {
            throw new \InvalidArgumentException('Register either the queue runtime or a legacy queue dispatch driver, not both.');
        }
        $this->operationProviders = is_array($operationProviders)
            ? array_values($operationProviders)
            : iterator_to_array($operationProviders, false);

        foreach ($this->grantedCapabilities ?? [] as $capability) {
            if (! is_string($capability) || $capability === '') {
                throw new \InvalidArgumentException('Granted capabilities must be non-empty strings.');
            }
        }
    }

    /**
     * @param array<string, mixed> $params
     * @return array<string, mixed>
     */
    public function invoke(string $method, array $params): array
    {
        return match ($method) {
            'flow.definition' => $this->definition($params),
            'flow.get' => $this->get($params),
            'flow.start' => $this->start($params),
            'flow.complete' => $this->complete($params),
            'flow.resume' => $this->resume($params),
            'flow.cancel' => $this->cancel($params),
            'flow.validate' => $this->validateDefinition($params),
            'flow.operations' => $this->operations($params),
            'ows.validate' => $this->owsValidate($params),
            'ows.register' => $this->owsRegister($params),
            default => throw new RpcFailure(-32601, 'Method not found'),
        };
    }

    /**
     * @param array<string, mixed> $params
     * @return array<string, mixed>
     */
    private function definition(array $params): array
    {
        return ['definition' => $this->definitions->definition($this->flowKey($params))->toArray()];
    }

    /**
     * @param array<string, mixed> $params
     * @return array<string, mixed>
     */
    private function get(array $params): array
    {
        $subject = $this->subject($params);
        $flowKey = $this->flowKey($params);

        $state = $this->store($subject, $params)->get($flowKey);
        $queue = $this->queueFor($state);
        if ($queue !== null) {
            assert($state !== null);
            $state = $queue->get($subject, $state);
        }
        return ['state' => $this->publicState($state)];
    }

    /**
     * @param array<string, mixed> $params
     * @return array<string, mixed>
     */
    private function start(array $params): array
    {
        $subject = $this->subject($params);
        $flowKey = $this->flowKey($params);
        $context = $this->object($params['context'] ?? [], 'context');
        $store = $this->store($subject, $params);
        $driverProvided = array_key_exists('driver', $params);
        $driver = $params['driver'] ?? 'inline';

        if (! is_string($driver) || $driver === '') {
            throw new RpcFailure(-32602, 'Unknown or unavailable execution driver');
        }

        $created = false;
        $state = $store->mutate($flowKey, function (?FlowState $current) use ($flowKey, $context, $driver, $driverProvided, $store, $subject, &$created): FlowState {
            if ($current !== null) {
                $recorded = $current->metadata['execution_driver'] ?? null;
                if ($driverProvided && is_string($recorded) && $recorded !== $driver) {
                    throw new RpcFailure(-32009, "Flow run is already assigned to the '{$recorded}' driver");
                }

                return $current;
            }

            if (! ($this->execution->has($driver) || ($driver === 'queue' && $this->queue !== null))) {
                throw new RpcFailure(-32602, 'Unknown or unavailable execution driver');
            }
            $created = true;

            return (new FlowRun($store->runId, $this->definitions->definition($flowKey)))->initialState()->withMetadata([
                'execution_driver' => $driver,
                '_rpc_context' => $context,
                ...($driver === 'queue' && $this->queue !== null ? ['_rpc_authority' => 'queue-v1', 'execution_id' => RpcRunIdentity::executionId($subject, $flowKey, $store->runId)] : []),
            ]);
        });

        $queue = $this->queueFor($state);
        if ($queue !== null) {
            return ['state' => $this->publicState($queue->start($subject, $state))];
        }
        return $created ? $this->resume($params) : ['state' => $this->publicState($state)];
    }

    /**
     * @param array<string, mixed> $params
     * @return array<string, mixed>
     */
    private function complete(array $params): array
    {
        $subject = $this->subject($params);
        $flowKey = $this->flowKey($params);
        $stepId = $this->string($params, 'step_id');
        $idempotencyKey = $this->string($params, 'idempotency_key');
        $attempt = $params['attempt'] ?? null;
        if (! is_int($attempt) || $attempt < 1) {
            throw new RpcFailure(-32602, 'Invalid attempt');
        }
        $store = $this->store($subject, $params);
        $result = $this->object($params['result'] ?? null, 'result');

        $status = FlowStepStatus::tryFrom($this->string($result, 'status'));
        if (! in_array($status, [FlowStepStatus::COMPLETED, FlowStepStatus::FAILED, FlowStepStatus::SKIPPED], true)) {
            throw new RpcFailure(-32602, 'Invalid completion status');
        }

        $message = $result['message'] ?? null;
        $error = $result['error'] ?? null;
        $retriable = $result['retriable'] ?? null;
        $metadata = $this->object($result['metadata'] ?? [], 'result metadata');
        if (($message !== null && ! is_string($message)) || ($error !== null && ! is_string($error))) {
            throw new RpcFailure(-32602, 'Invalid completion message');
        }
        if ($retriable !== null && ! is_bool($retriable)) {
            throw new RpcFailure(-32602, 'Invalid retriable value');
        }

        // A present `output` carries any JSON value; the key's presence
        // distinguishes a structured null from no output at all.
        $output = null;
        if (array_key_exists('output', $result)) {
            $output = new StepOutput($result['output']);
        }

        $completion = new FlowDeferredCompletion(
            flowKey: $flowKey,
            stepId: $stepId,
            idempotencyKey: $idempotencyKey,
            result: new StepResult($status, message: $message, error: $error, retriable: $retriable, metadata: $metadata, output: $output),
            runId: $store->runId,
            attempt: $attempt,
        );
        $route = $store->get($flowKey);
        $queue = $this->queueFor($route);
        if ($queue !== null) {
            assert($route !== null);
            return ['state' => $this->publicState($queue->complete($subject, $route, $completion))];
        }
        $state = $store->mutate($flowKey, function (?FlowState $current) use ($completion): FlowState {
            $current = $this->available($current);

            $run = $current->run ?? throw new RpcFailure(-32004, 'Flow run not found');

            return $this->transitions->complete($run->definition, $current, $completion);
        });

        return ['state' => $this->publicState($state)];
    }

    /** @param array<string, mixed> $params
     *  @return array<string, mixed>
     */
    private function resume(array $params): array
    {
        $subject = $this->subject($params);
        $flowKey = $this->flowKey($params);
        $store = $this->store($subject, $params);
        $route = $store->get($flowKey);
        $queue = $this->queueFor($route);
        if ($queue !== null) {
            assert($route !== null);
            return ['state' => $this->publicState($queue->resume($subject, $route))];
        }
        $claim = bin2hex(random_bytes(16));
        $state = $store->mutate($flowKey, function (?FlowState $current) use ($claim): FlowState {
            $current = $this->available($current);
            if (in_array($current->status, [FlowStatus::COMPLETED, FlowStatus::CANCELLED], true)) {
                return $current;
            }

            return $current->withMetadata(['_rpc_claim' => $claim]);
        });

        if (($state->metadata['_rpc_claim'] ?? null) !== $claim) {
            return ['state' => $this->publicState($state)];
        }

        // Deliberately outside mutate: a rollback cannot undo an external effect.
        // If dispatch or persistence fails, retain the claim for host reconciliation.
        $run = $state->run ?? throw new RpcFailure(-32004, 'Flow run not found');
        $driver = $state->metadata['execution_driver'] ?? 'inline';
        if (! is_string($driver) || ! $this->execution->has($driver)) {
            throw new RpcFailure(-32602, 'The stored execution driver is unavailable on this host');
        }
        $executionId = $state->metadata['execution_id']
            ?? $this->executionId($subject, $run->definition, $store->runId);
        $receipt = $this->execution->dispatch(new FlowExecutionRequest(
            definition: $run->definition,
            state: $state,
            context: $this->object($state->metadata['_rpc_context'] ?? [], 'stored context'),
            driver: $driver,
            executionId: is_string($executionId) && $executionId !== ''
                ? $executionId
                : $this->executionId($subject, $run->definition, $store->runId),
            subject: $subject,
        ));
        $next = ($receipt->state ?? $state)->withMetadata([
            'execution_driver' => $receipt->driver,
            'execution_status' => $receipt->status->value,
            'execution_id' => $receipt->executionId,
        ]);
        $state = $store->mutate($flowKey, static function (?FlowState $current) use ($claim, $next, $receipt): FlowState {
            if (($current?->metadata['_rpc_claim'] ?? null) !== $claim) {
                throw new RpcFailure(-32009, 'Flow execution claim has changed');
            }
            // Inline execution owns the returned state. An async receipt only
            // acknowledges dispatch, so preserve any authoritative state a
            // fast worker may already have written before this final save.
            $authoritative = $receipt->status === \Webong\WorkFlow\Enums\FlowExecutionStatus::DISPATCHED
                ? $current
                : $next;

            return $authoritative->withMetadata([
                'execution_driver' => $receipt->driver,
                'execution_status' => $receipt->status->value,
                'execution_id' => $receipt->executionId,
                '_rpc_claim' => null,
            ]);
        });

        return ['state' => $this->publicState($state)];
    }

    /** @param array<string, mixed> $params
     *  @return array<string, mixed>
     */
    private function cancel(array $params): array
    {
        $subject = $this->subject($params);
        $route = $this->store($subject, $params)->get($this->flowKey($params));
        $queue = $this->queueFor($route);
        if ($queue !== null) {
            assert($route !== null);
            return ['state' => $this->publicState($queue->cancel($subject, $route))];
        }
        $state = $this->store($this->subject($params), $params)->mutate(
            $this->flowKey($params),
            fn (?FlowState $current): FlowState => $this->transitions->cancel($this->available($current)),
        );

        return ['state' => $this->publicState($state)];
    }

    /**
     * Validate an OWS document without translating. Only available when the
     * host explicitly enables OWS support (ADR 0006: registration stays
     * host-controlled; the RPC surface is not an upload endpoint by default).
     *
     * @param array<string, mixed> $params
     * @return array<string, mixed>
     */
    private function owsValidate(array $params): array
    {
        $report = $this->ows()->validateOnly($this->document($params));

        return ['report' => $report->toArray()];
    }

    /**
     * Validate and translate an OWS document. The report carries the
     * CapabilityReport and the translated FlowDefinition; persisting it
     * remains the host's decision.
     *
     * @param array<string, mixed> $params
     * @return array<string, mixed>
     */
    private function owsRegister(array $params): array
    {
        $report = $this->ows()->registerOwsDefinition($this->document($params));

        return ['report' => $report->toArray()];
    }

    private function ows(): OpenWorkflow
    {
        return $this->ows ?? throw new RpcFailure(
            -32601,
            'OWS document registration is not enabled on this host',
        );
    }

    /**
     * Validate a canonical flow definition without executing, registering,
     * or persisting it. The definition travels in the request, so an author
     * can check a document this server has never seen. Always available:
     * this is a pure function over the request plus the host's bound
     * executors (ADR 0010).
     *
     * @param array<string, mixed> $params
     * @return array<string, mixed>
     */
    private function validateDefinition(array $params): array
    {
        $definition = $params['definition'] ?? null;
        if (! is_array($definition)) {
            throw new RpcFailure(-32602, 'Invalid definition');
        }

        if (array_key_exists('capabilities', $params)) {
            throw new RpcFailure(-32602, 'Capabilities are configured by the host');
        }

        $report = (new FlowDefinitionValidator($this->operationProviders, $this->grantedCapabilities))->validate($definition);

        return $report->toArray();
    }

    /**
     * Read-only operation catalog for one flow: the descriptors host
     * executors publish for the definition's steps. Available only when
     * the host binds operation providers (ADR 0009: the catalog is
     * authoring data, not an execution or upload endpoint).
     *
     * @param array<string, mixed> $params
     * @return array<string, mixed>
     */
    private function operations(array $params): array
    {
        if ($this->operationProviders === []) {
            throw new RpcFailure(-32601, 'The operation catalog is not enabled on this host');
        }

        $definition = $this->definitions->definition($this->flowKey($params));
        $operations = (new FlowOperationCatalog())->describe($definition, $this->operationProviders);

        return ['operations' => array_map(
            static fn (FlowOperation $operation): array => $operation->toArray(),
            $operations,
        )];
    }

    /** @param array<string, mixed> $params */
    private function document(array $params): string
    {
        $document = $params['document'] ?? null;
        if (! is_string($document) || trim($document) === '') {
            throw new RpcFailure(-32602, 'Invalid document');
        }

        return $document;
    }

    private function queueFor(?FlowState $route): ?QueueRpcRuntime
    {
        if (($route?->metadata['_rpc_authority'] ?? null) === 'queue-v1') {
            return $this->queue ?? throw new RpcFailure(-32602, 'Queue authority is unavailable on this host');
        }
        if ($this->queue !== null && ($route?->metadata['execution_driver'] ?? null) === 'queue') {
            throw new RpcFailure(-32009, 'Legacy queue runs require explicit migration');
        }
        return null;
    }

    private function available(?FlowState $state): FlowState
    {
        if ($this->queueFor($state) !== null) {
            throw new RpcFailure(-32009, 'Queue runs must use queue authority; retry request');
        }
        if ($state?->run === null) {
            throw new RpcFailure(-32004, 'Flow run not found');
        }
        if (($state->metadata['_rpc_claim'] ?? null) !== null) {
            throw new RpcFailure(-32009, 'Flow run has an active or interrupted execution claim');
        }

        return $state;
    }

    /** @param array<string, mixed> $params */
    private function store(FlowStateSubject $subject, array $params): RunScopedFlowStateStore
    {
        return new RunScopedFlowStateStore($this->stores->for($subject), $this->string($params, 'run_id'));
    }

    /** @return array<string, mixed>|null */
    private function publicState(?FlowState $state): ?array
    {
        if ($state === null) {
            return null;
        }
        $data = $state->toArray();
        $metadata = $state->metadata;
        unset($metadata['_rpc_context'], $metadata['_rpc_claim'], $metadata['_rpc_authority'], $metadata['_queue_storage_id'], $metadata['_rpc_import']);
        $data['metadata'] = $metadata;
        $data['execution_claim'] = $state->metadata['_rpc_claim'] ?? null;

        return $data;
    }

    /** @param array<string, mixed> $params */
    private function flowKey(array $params): string
    {
        return $this->string($params, 'flow_key');
    }

    private function executionId(FlowStateSubject $subject, FlowDefinition $definition, string $runId): string
    {
        return RpcRunIdentity::executionId($subject, $definition->key, $runId);
    }

    /** @param array<string, mixed> $params */
    private function subject(array $params): FlowStateSubject
    {
        $subject = $this->object($params['subject'] ?? null, 'subject');

        return new FlowStateSubject(
            type: $this->string($subject, 'type'),
            id: $this->string($subject, 'id'),
        );
    }

    /** @param array<string, mixed> $params */
    private function string(array $params, string $key): string
    {
        $value = $params[$key] ?? null;
        if (! is_string($value) || $value === '') {
            throw new RpcFailure(-32602, "Invalid {$key}");
        }

        return $value;
    }

    /** @return array<string, mixed> */
    private function object(mixed $value, string $name): array
    {
        if (! is_array($value)) {
            throw new RpcFailure(-32602, "Invalid {$name}");
        }

        foreach ($value as $key => $_) {
            if (! is_string($key)) {
                throw new RpcFailure(-32602, "Invalid {$name}");
            }
        }

        /** @var array<string, mixed> $value */
        return $value;
    }
}
