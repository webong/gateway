<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver\Tests\App;

use InvalidArgumentException;
use PHPUnit\Framework\TestCase;
use Webong\WorkFlow\Contracts\FlowDefinitionProvider;
use Webong\WorkFlow\Contracts\FlowExecutionDriver;
use Webong\WorkFlow\Contracts\FlowOperationProvider;
use Webong\WorkFlow\Contracts\FlowStateStoreFactory;
use Webong\WorkFlow\Contracts\FlowStepExecutor;
use Webong\WorkFlow\Contracts\ForgettableFlowStateStore;
use Webong\Gateway\Weaver\FlowRpcApplication;
use Webong\Gateway\Weaver\RpcFailure;
use Webong\WorkFlow\OpenWorkflow\OpenWorkflow;
use Webong\WorkFlow\Services\FlowExecutionDispatcher;
use Webong\WorkFlow\Services\FlowRunner;
use Webong\WorkFlow\Services\InMemoryFlowStateStore;
use Webong\WorkFlow\Services\InlineFlowExecutionDriver;
use Webong\WorkFlow\Enums\FlowExecutionStatus;
use Webong\WorkFlow\Temporal\TemporalFlowExecutionDriver;
use Webong\WorkFlow\ValueObjects\FlowDefinition;
use Webong\WorkFlow\ValueObjects\FlowExecutionReceipt;
use Webong\WorkFlow\ValueObjects\FlowExecutionRequest;
use Webong\WorkFlow\ValueObjects\FlowOperation;
use Webong\WorkFlow\ValueObjects\FlowStateSubject;
use Webong\WorkFlow\ValueObjects\FlowStepDefinition;
use Webong\WorkFlow\ValueObjects\StepResult;
use Webong\WorkFlow\ValueObjects\StepState;

final class FlowRpcApplicationTest extends TestCase
{
    public function testItStartsReadsAndCompletesADeferredFlowIdempotently(): void
    {
        $executor = new class implements FlowStepExecutor {
            public int $calls = 0;

            public function supports(FlowStepDefinition $step): bool
            {
                return $step->id === 'approve';
            }

            public function execute(\Webong\WorkFlow\ValueObjects\FlowStepInvocation $invocation): StepResult
            {
                $this->calls++;

                return StepResult::deferred('Awaiting approval');
            }
        };

        $application = $this->application($executor);
        $params = ['flow_key' => 'demo', 'run_id' => 'run-1', 'subject' => ['type' => 'order', 'id' => '42']];

        self::assertNull($application->invoke('flow.get', $params)['state']);
        $started = $application->invoke('flow.start', $params);
        self::assertSame('pending', $started['state']['steps']['approve']['status']);
        self::assertSame('inline', $started['state']['metadata']['execution_driver']);

        $application->invoke('flow.start', $params);
        self::assertSame(1, $executor->calls);

        $complete = [...$params, 'step_id' => 'approve', 'attempt' => 1, 'idempotency_key' => 'approval-1', 'result' => ['status' => 'completed']];
        $completed = $application->invoke('flow.complete', $complete);
        self::assertSame('completed', $completed['state']['steps']['approve']['status']);
        self::assertSame($completed, $application->invoke('flow.complete', $complete));
        self::assertSame($completed, $application->invoke('flow.get', $params));
    }

    public function testItRejectsUnknownMethodsAndUnregisteredExecutionDrivers(): void
    {
        $application = $this->application(new class implements FlowStepExecutor {
            public function supports(FlowStepDefinition $step): bool
            {
                return true;
            }

            public function execute(\Webong\WorkFlow\ValueObjects\FlowStepInvocation $invocation): StepResult
            {
                return StepResult::completed();
            }
        });

        try {
            $application->invoke('flow.unknown', []);
            self::fail('Unknown method was accepted');
        } catch (RpcFailure $exception) {
            self::assertSame(-32601, $exception->rpcCode);
        }

        $this->expectException(RpcFailure::class);
        $application->invoke('flow.start', [
            'flow_key' => 'demo',
            'subject' => ['type' => 'order', 'id' => '42'],
            'driver' => 'temporal',
        ]);
    }

    public function test_it_starts_a_registered_async_driver_once_and_persists_its_receipt(): void
    {
        $driver = new class implements FlowExecutionDriver {
            public int $calls = 0;
            public ?FlowExecutionRequest $request = null;

            public function name(): string
            {
                return 'queue';
            }

            public function dispatch(FlowExecutionRequest $request): FlowExecutionReceipt
            {
                $this->calls++;
                $this->request = $request;

                return new FlowExecutionReceipt(
                    driver: 'queue',
                    status: FlowExecutionStatus::DISPATCHED,
                    executionId: 'job-42',
                    state: $request->state,
                );
            }
        };
        $application = $this->application($this->noopExecutor(), drivers: [$driver]);
        $params = [
            'flow_key' => 'demo',
            'run_id' => 'run-1',
            'subject' => ['type' => 'order', 'id' => '42'],
            'driver' => 'queue',
            'context' => ['source' => 'rpc'],
        ];

        $started = $application->invoke('flow.start', $params);

        self::assertSame(1, $driver->calls);
        self::assertSame('queue', $driver->request?->driver);
        self::assertSame(['source' => 'rpc'], $driver->request?->context);
        self::assertSame('queue', $started['state']['metadata']['execution_driver']);
        self::assertSame('dispatched', $started['state']['metadata']['execution_status']);
        self::assertSame('job-42', $started['state']['metadata']['execution_id']);
        self::assertNull($started['state']['execution_claim']);

        $retry = $params;
        unset($retry['driver'], $retry['context']);
        self::assertSame($started, $application->invoke('flow.start', $retry));
        self::assertSame(1, $driver->calls, 'An idempotent start must not enqueue a second job.');
    }

    public function test_it_starts_the_registered_temporal_driver_with_a_scoped_execution_identity(): void
    {
        $workflowId = null;
        $payload = null;
        $driver = new TemporalFlowExecutionDriver(
            static function (string $id, array $input) use (&$workflowId, &$payload): string {
                $workflowId = $id;
                $payload = $input;

                return $id;
            },
        );
        $application = $this->application($this->noopExecutor(), drivers: [$driver]);

        $started = $application->invoke('flow.start', [
            'flow_key' => 'demo',
            'run_id' => 'run-1',
            'subject' => ['type' => 'order', 'id' => '42'],
            'driver' => 'temporal',
            'context' => ['source' => 'rpc'],
        ]);

        self::assertIsString($workflowId);
        self::assertStringStartsWith('workflow-rpc:', $workflowId);
        self::assertNotSame('run-1', $workflowId);
        self::assertSame($workflowId, $started['state']['metadata']['execution_id']);
        self::assertIsArray($payload);
        self::assertSame('temporal', $payload['state']['metadata']['execution_driver']);
        self::assertSame(['source' => 'rpc'], $payload['context']);

        $firstWorkflowId = $workflowId;
        $application->invoke('flow.start', [
            'flow_key' => 'demo',
            'run_id' => 'run-1',
            'subject' => ['type' => 'order', 'id' => '43'],
            'driver' => 'temporal',
        ]);
        self::assertNotSame($firstWorkflowId, $workflowId);
    }

    public function test_runs_pin_the_definition_and_context_and_resume_explicitly(): void
    {
        $provider = new class implements FlowDefinitionProvider {
            public int $version = 1;
            public function definition(string $flowKey): FlowDefinition
            {
                return new FlowDefinition('demo', [
                    new FlowStepDefinition('approve', 'Approve'),
                    new FlowStepDefinition('notify-v'.$this->version, 'Notify', dependsOn: ['approve']),
                ], version: $this->version);
            }
        };
        $executor = new class implements FlowStepExecutor {
            public array $calls = [];
            public function supports(FlowStepDefinition $step): bool { return true; }
            public function execute(\Webong\WorkFlow\ValueObjects\FlowStepInvocation $invocation): StepResult
            {
                $this->calls[] = [$invocation->step->id, $invocation->context->get('source')];
                return $invocation->step->id === 'approve' ? StepResult::deferred() : StepResult::completed();
            }
        };
        $app = $this->application($executor, $provider);
        $params = ['flow_key' => 'demo', 'run_id' => 'one', 'subject' => ['type' => 'order', 'id' => '42']];
        $started = $app->invoke('flow.start', [...$params, 'context' => ['source' => 'original']]);
        self::assertArrayNotHasKey('_rpc_context', $started['state']['metadata']);
        $app->invoke('flow.resume', $params);
        self::assertCount(1, $executor->calls);
        $provider->version = 2;
        $completed = $app->invoke('flow.complete', [...$params, 'step_id' => 'approve', 'attempt' => 1, 'idempotency_key' => 'approved', 'result' => ['status' => 'completed']]);
        self::assertSame('running', $completed['state']['status']);
        self::assertCount(1, $executor->calls);
        $resumed = $app->invoke('flow.resume', $params);
        self::assertSame('completed', $resumed['state']['status']);
        self::assertSame(1, $resumed['state']['run']['definition']['version']);
        self::assertSame(['notify-v1', 'original'], $executor->calls[1]);
        $app->invoke('flow.resume', $params);
        self::assertCount(2, $executor->calls);
        $second = $app->invoke('flow.start', [...$params, 'run_id' => 'two']);
        self::assertSame(2, $second['state']['run']['definition']['version']);
        self::assertSame($resumed, $app->invoke('flow.get', $params));
    }

    public function test_execution_runs_outside_mutation_and_a_claim_prevents_overlapping_passes(): void
    {
        $storage = new class implements ForgettableFlowStateStore {
            public bool $locked = false;
            private array $states = [];
            public function get(string $flowKey): ?\Webong\WorkFlow\ValueObjects\FlowState { return $this->states[$flowKey] ?? null; }
            public function put(string $flowKey, \Webong\WorkFlow\ValueObjects\FlowState $state): void { $this->states[$flowKey] = $state; }
            public function forget(string $flowKey): void { unset($this->states[$flowKey]); }
            public function mutate(string $flowKey, \Closure $transition): \Webong\WorkFlow\ValueObjects\FlowState
            {
                self::assertUnlocked($this->locked);
                $this->locked = true;
                try {
                    $state = $transition($this->get($flowKey));
                    $this->put($flowKey, $state);
                    return $state;
                } finally {
                    $this->locked = false;
                }
            }
            private static function assertUnlocked(bool $locked): void
            {
                if ($locked) { throw new \RuntimeException('Nested lock'); }
            }
        };
        $stores = new class($storage) implements FlowStateStoreFactory {
            public function __construct(private ForgettableFlowStateStore $storage) {}
            public function for(FlowStateSubject $subject): ForgettableFlowStateStore { return $this->storage; }
        };
        $params = ['flow_key' => 'demo', 'run_id' => 'one', 'subject' => ['type' => 'order', 'id' => '42']];
        $executor = new class($storage, $params) implements FlowStepExecutor {
            public ?FlowRpcApplication $app = null;
            public bool $sawConflict = false;
            public function __construct(private ForgettableFlowStateStore $storage, private array $params) {}
            public function supports(FlowStepDefinition $step): bool { return true; }
            public function execute(\Webong\WorkFlow\ValueObjects\FlowStepInvocation $invocation): StepResult
            {
                TestCase::assertFalse($this->storage->locked);
                try {
                    $this->app->invoke('flow.resume', $this->params);
                    TestCase::fail('Overlapping pass accepted');
                } catch (RpcFailure $exception) {
                    $this->sawConflict = $exception->rpcCode === -32009;
                }
                return StepResult::deferred();
            }
        };
        $app = $executor->app = $this->application($executor, stores: $stores);
        $app->invoke('flow.start', $params);
        self::assertTrue($executor->sawConflict);
        $cancelled = $app->invoke('flow.cancel', $params);
        self::assertSame('cancelled', $cancelled['state']['status']);
        self::assertSame($cancelled, $app->invoke('flow.resume', $params));
    }

    public function test_failed_result_persistence_leaves_a_claim_and_does_not_replay(): void
    {
        $storage = new class implements ForgettableFlowStateStore {
            private InMemoryFlowStateStore $inner;
            public function __construct() { $this->inner = new InMemoryFlowStateStore(); }
            public function get(string $flowKey): ?\Webong\WorkFlow\ValueObjects\FlowState { return $this->inner->get($flowKey); }
            public function put(string $flowKey, \Webong\WorkFlow\ValueObjects\FlowState $state): void { $this->inner->put($flowKey, $state); }
            public function forget(string $flowKey): void { $this->inner->forget($flowKey); }
            public function mutate(string $flowKey, \Closure $transition): \Webong\WorkFlow\ValueObjects\FlowState
            {
                return $this->inner->mutate($flowKey, static function (?\Webong\WorkFlow\ValueObjects\FlowState $current) use ($transition): \Webong\WorkFlow\ValueObjects\FlowState {
                    $next = $transition($current);
                    if (($current?->metadata['_rpc_claim'] ?? null) !== null && ($next->metadata['_rpc_claim'] ?? null) === null) {
                        throw new \RuntimeException('Simulated database outage on final save');
                    }
                    return $next;
                });
            }
        };
        $stores = new class($storage) implements FlowStateStoreFactory {
            public function __construct(private ForgettableFlowStateStore $storage) {}
            public function for(FlowStateSubject $subject): ForgettableFlowStateStore { return $this->storage; }
        };
        $executor = new class implements FlowStepExecutor {
            public int $calls = 0;
            public function supports(FlowStepDefinition $step): bool { return true; }
            public function execute(\Webong\WorkFlow\ValueObjects\FlowStepInvocation $invocation): StepResult
            {
                $this->calls++;
                return StepResult::completed();
            }
        };
        $app = $this->application($executor, stores: $stores);
        $params = ['flow_key' => 'demo', 'run_id' => 'one', 'subject' => ['type' => 'order', 'id' => '42']];
        try {
            $app->invoke('flow.start', $params);
            self::fail('Final save did not fail');
        } catch (\RuntimeException $exception) {
            self::assertSame('Simulated database outage on final save', $exception->getMessage());
        }
        $saved = $app->invoke('flow.get', $params);
        self::assertNotNull($saved['state']['execution_claim']);
        self::assertSame($saved, $app->invoke('flow.start', $params));
        try {
            $app->invoke('flow.resume', $params);
            self::fail('Interrupted execution was replayed');
        } catch (RpcFailure $exception) {
            self::assertSame(-32009, $exception->rpcCode);
        }
        self::assertSame(1, $executor->calls);
    }

    /** @param list<FlowExecutionDriver> $drivers */
    private function application(FlowStepExecutor $executor, ?FlowDefinitionProvider $definitions = null, ?FlowStateStoreFactory $stores = null, bool $ows = false, bool $operations = false, array $drivers = []): FlowRpcApplication
    {
        $definitions ??= new class implements FlowDefinitionProvider {
            public function definition(string $flowKey): FlowDefinition
            {
                if ($flowKey !== 'demo') {
                    throw new InvalidArgumentException('Unknown flow');
                }

                return new FlowDefinition('demo', [new FlowStepDefinition('approve', 'Approve')]);
            }
        };

        $stores ??= new class implements FlowStateStoreFactory {
            /** @var array<string, InMemoryFlowStateStore> */
            private array $states = [];

            public function for(FlowStateSubject $subject): ForgettableFlowStateStore
            {
                $key = $subject->type.':'.$subject->id;

                return $this->states[$key] ??= new InMemoryFlowStateStore();
            }
        };

        return new FlowRpcApplication(
            $definitions,
            $stores,
            new FlowExecutionDispatcher([new InlineFlowExecutionDriver(new FlowRunner(), [$executor]), ...$drivers]),
            ows: $ows ? new OpenWorkflow() : null,
            operationProviders: $operations ? [$executor] : [],
        );
    }

    public function testOperationCatalogIsOptInAndReturnsStepDescriptors(): void
    {
        // Without a bound provider the method does not exist for this host.
        $application = $this->application($this->noopExecutor(), operations: false);
        try {
            $application->invoke('flow.operations', ['flow_key' => 'demo']);
            self::fail('The catalog must be opt-in.');
        } catch (RpcFailure $exception) {
            self::assertSame(-32601, $exception->rpcCode);
            self::assertSame('The operation catalog is not enabled on this host', $exception->getMessage());
        }

        $provider = new class implements FlowStepExecutor, FlowOperationProvider {
            public function supports(FlowStepDefinition $step): bool
            {
                return $step->id === 'approve';
            }

            public function execute(\Webong\WorkFlow\ValueObjects\FlowStepInvocation $invocation): StepResult
            {
                return StepResult::deferred('Awaiting approval');
            }

            public function operation(FlowStepDefinition $step): ?FlowOperation
            {
                return new FlowOperation(
                    key: 'demo.approve',
                    label: 'Approve',
                    inputSchema: ['type' => 'object'],
                    capabilities: ['approval'],
                );
            }
        };

        $enabled = $this->application($provider, operations: true);
        $report = $enabled->invoke('flow.operations', ['flow_key' => 'demo']);
        self::assertSame(['approve'], array_keys($report['operations']));
        self::assertSame('demo.approve', $report['operations']['approve']['key']);
        self::assertSame(['approval'], $report['operations']['approve']['capabilities']);
    }

    public function testOwsMethodsAreUnavailableUnlessTheHostEnablesThem(): void
    {
        $application = $this->application($this->noopExecutor(), ows: false);

        $this->expectException(RpcFailure::class);
        $this->expectExceptionMessage('OWS document registration is not enabled');
        $application->invoke('ows.validate', ['document' => 'version: "1.0.3"']);
    }

    public function testOwsValidateReturnsACapabilityReport(): void
    {
        $application = $this->application($this->noopExecutor(), ows: true);
        $yaml = <<<'YAML'
version: "1.0.3"
document:
  dsl: "1.0.3"
  namespace: com.example
  name: rpc-flow
  version: "1.0.0"
do:
  - first:
      call: firstTask
YAML;

        $result = $application->invoke('ows.validate', ['document' => $yaml]);

        self::assertTrue($result['report']['valid']);
        self::assertSame('1.0.3', $result['report']['version']);
        self::assertNull($result['report']['flow_definition']); // validateOnly does not translate
    }

    public function testOwsRegisterReturnsTheTranslatedDefinition(): void
    {
        $application = $this->application($this->noopExecutor(), ows: true);
        $yaml = <<<'YAML'
version: "1.0.3"
document:
  dsl: "1.0.3"
  namespace: com.example
  name: rpc-flow
  version: "1.0.0"
do:
  - first:
      call: firstTask
  - second:
      call: secondTask
YAML;

        $result = $application->invoke('ows.register', ['document' => $yaml]);

        self::assertTrue($result['report']['valid']);
        self::assertNotNull($result['report']['flow_definition']);
        self::assertSame('com.example/rpc-flow@1.0.0', $result['report']['flow_definition']['key']);
        self::assertCount(2, $result['report']['flow_definition']['steps']);
    }

    public function testOwsRegisterRejectsAnInvalidDocument(): void
    {
        $application = $this->application($this->noopExecutor(), ows: true);

        $this->expectException(RpcFailure::class);
        $this->expectExceptionMessage('Invalid document');
        $application->invoke('ows.register', ['document' => '   ']);
    }

    private function noopExecutor(): FlowStepExecutor
    {
        return new class implements FlowStepExecutor {
            public function supports(FlowStepDefinition $step): bool
            {
                return false;
            }

            public function execute(\Webong\WorkFlow\ValueObjects\FlowStepInvocation $invocation): StepResult
            {
                return StepResult::completed();
            }
        };
    }

    public function test_completion_output_round_trips_through_the_rpc_state(): void
    {
        $executor = new class implements FlowStepExecutor {
            public function supports(FlowStepDefinition $step): bool
            {
                return $step->id === 'approve';
            }

            public function execute(\Webong\WorkFlow\ValueObjects\FlowStepInvocation $invocation): StepResult
            {
                return StepResult::deferred('Awaiting approval');
            }
        };
        $application = $this->application($executor);
        $params = ['flow_key' => 'demo', 'run_id' => 'run-1', 'subject' => ['type' => 'order', 'id' => '42']];
        $application->invoke('flow.start', $params);

        // A structured object output.
        $object = $application->invoke('flow.complete', [...$params, 'step_id' => 'approve', 'attempt' => 1,
            'idempotency_key' => 'event-1', 'result' => ['status' => 'completed', 'output' => ['total' => 100]]]);
        self::assertSame(['total' => 100], $object['state']['steps']['approve']['output']);

        // The stored state keeps the output for later reads.
        self::assertSame(['total' => 100], $application->invoke('flow.get', $params)['state']['steps']['approve']['output']);

        // A present JSON null output stays distinguishable from an absent one.
        $params2 = [...$params, 'run_id' => 'run-2'];
        $application->invoke('flow.start', $params2);
        $null = $application->invoke('flow.complete', [...$params2, 'step_id' => 'approve', 'attempt' => 1,
            'idempotency_key' => 'event-2', 'result' => ['status' => 'completed', 'output' => null]]);
        self::assertArrayHasKey('output', $null['state']['steps']['approve']);
        self::assertNull($null['state']['steps']['approve']['output']);

        $params3 = [...$params, 'run_id' => 'run-3'];
        $application->invoke('flow.start', $params3);
        $absent = $application->invoke('flow.complete', [...$params3, 'step_id' => 'approve', 'attempt' => 1,
            'idempotency_key' => 'event-3', 'result' => ['status' => 'completed']]);
        self::assertArrayNotHasKey('output', $absent['state']['steps']['approve']);
    }
}
