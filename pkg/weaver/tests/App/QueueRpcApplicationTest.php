<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver\Tests\App;

use PDO;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Webong\Gateway\Weaver\FlowRpcApplication;
use Webong\Gateway\Weaver\PdoMySqlFlowStateStoreFactory;
use Webong\Gateway\Weaver\PdoPostgresFlowStateStoreFactory;
use Webong\Gateway\Weaver\PdoSqliteFlowStateStoreFactory;
use Webong\Gateway\Weaver\QueueRpcRuntime;
use Webong\Gateway\Weaver\QueueScheduler;
use Webong\Gateway\Weaver\QueueWorker;
use Webong\Gateway\Weaver\RpcFailure;
use Webong\WorkFlow\Contracts\FlowDefinitionProvider;
use Webong\WorkFlow\Contracts\FlowStateStoreFactory;
use Webong\WorkFlow\Contracts\FlowStepExecutor;
use Webong\WorkFlow\Services\DefaultFlowStateSerializer;
use Webong\WorkFlow\Services\FlowExecutionDispatcher;
use Webong\WorkFlow\Services\FlowRunner;
use Webong\WorkFlow\Services\InlineFlowExecutionDriver;
use Webong\WorkFlow\Services\RunScopedFlowStateStore;
use Webong\WorkFlow\ValueObjects\FlowDefinition;
use Webong\WorkFlow\ValueObjects\FlowInputBinding;
use Webong\WorkFlow\ValueObjects\FlowRun;
use Webong\WorkFlow\ValueObjects\FlowStateSubject;
use Webong\WorkFlow\ValueObjects\FlowStepDefinition;
use Webong\WorkFlow\ValueObjects\FlowStepInvocation;
use Webong\WorkFlow\ValueObjects\StepResult;

final class QueueRpcApplicationTest extends TestCase
{
    private PDO $pdo;
    private QueueScheduler $queue;
    private FlowRpcApplication $app;
    private FlowStateStoreFactory $stores;
    private FlowDefinitionProvider $definitions;
    private FlowStepExecutor $executor;
    private QueueWorker $worker;
    private array $calls = [];
    private int $version = 1;
    private string $mode = 'deferred';
    private int $now;

    public static function backends(): array { return [['sqlite'], ['postgres'], ['mysql']]; }

    private function open(string $backend): void
    {
        if ($backend === 'sqlite') {
            $this->pdo = new PDO('sqlite::memory:');
        } else {
            $key = $backend === 'postgres' ? 'POSTGRES' : 'MYSQL';
            $dsn = getenv('WORK_FLOW_' . $key . '_DSN');
            if (! $dsn) { $this->fail('SQL backend not configured'); }
            $this->pdo = new PDO($dsn, getenv('WORK_FLOW_' . $key . '_USER'), getenv('WORK_FLOW_' . $key . '_PASSWORD'));
        }
        $this->pdo->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);
        $this->drop();
        $this->pdo->exec('DROP TABLE IF EXISTS workflow_rpc_states');
        $this->pdo->exec(file_get_contents(dirname(__DIR__, 2) . '/schema/' . $backend . '.sql'));
        $this->pdo->exec('DELETE FROM workflow_rpc_states');
        QueueScheduler::install($this->pdo, 4);
        $this->now = time();
        $this->queue = new QueueScheduler($this->pdo, fn (): int => $this->now);
        $this->stores = match ($backend) {
            'postgres' => new PdoPostgresFlowStateStoreFactory($this->pdo, new DefaultFlowStateSerializer()),
            'mysql' => new PdoMySqlFlowStateStoreFactory($this->pdo, new DefaultFlowStateSerializer()),
            'sqlite' => new PdoSqliteFlowStateStoreFactory($this->pdo, new DefaultFlowStateSerializer()),
        };
        $this->definitions = new class($this) implements FlowDefinitionProvider {
            public function __construct(private QueueRpcApplicationTest $test) {}
            public function definition(string $flowKey): FlowDefinition { return $this->test->definition($flowKey); }
        };
        $this->executor = new class($this) implements FlowStepExecutor {
            public function __construct(private QueueRpcApplicationTest $test) {}
            public function supports(FlowStepDefinition $step): bool { return true; }
            public function execute(FlowStepInvocation $invocation): StepResult { return $this->test->execute($invocation); }
        };
        $this->app = $this->application();
        $this->worker = new QueueWorker($this->queue, [$this->executor]);
    }

    public function definition(string $flowKey): FlowDefinition
    {
        return new FlowDefinition($flowKey, [
            new FlowStepDefinition('approve', 'Approve', input: ['source' => FlowInputBinding::context('/source')]),
            new FlowStepDefinition('notify-v' . $this->version, 'Notify', dependsOn: ['approve']),
        ], version: $this->version);
    }

    public function execute(FlowStepInvocation $invocation): StepResult
    {
        $this->assertFalse($this->pdo->inTransaction(), 'Executor ran under the RPC routing lock');
        $this->calls[] = [$invocation->runId, $invocation->flowKey, $invocation->step->id, $invocation->attempt, $invocation->context->get('source')];
        if ($invocation->step->id !== 'approve') { return StepResult::completed(); }
        return match ($this->mode) {
            'pending' => StepResult::pending(),
            'deadline' => StepResult::deferred(deadlineAt: $this->now + 5, metadata: ['deadline_transition' => 'complete']),
            default => StepResult::deferred(),
        };
    }

    private function application(bool $queue = true): FlowRpcApplication
    {
        return new FlowRpcApplication($this->definitions, $this->stores,
            new FlowExecutionDispatcher([new InlineFlowExecutionDriver(new FlowRunner(), [$this->executor])]),
            queue: $queue ? new QueueRpcRuntime($this->queue) : null);
    }

    private function params(string $subject = 'one', string $flow = 'orders'): array
    {
        return ['flow_key' => $flow, 'run_id' => 'logical-1', 'subject' => ['type' => 'order', 'id' => $subject]];
    }

    private function start(array $params): array
    {
        return $this->app->invoke('flow.start', [...$params, 'driver' => 'queue', 'context' => ['source' => 'original']])['state'];
    }

    private function completion(array $params, mixed $output = null): array
    {
        return [...$params, 'step_id' => 'approve', 'attempt' => 1, 'idempotency_key' => 'approved', 'result' => ['status' => 'completed', 'output' => $output]];
    }

    private function reservation(array $params): RunScopedFlowStateStore
    {
        return new RunScopedFlowStateStore($this->stores->for(new FlowStateSubject($params['subject']['type'], $params['subject']['id'])), $params['run_id']);
    }

    #[DataProvider('backends')]
    public function testQueueLifecycleUsesAuthorityWhileRoutingSnapshotStaysPending(string $backend): void
    {
        $this->open($backend);
        $params = $this->params();
        $this->assertNull($this->app->invoke('flow.get', $params)['state']);
        $started = $this->start($params);
        $this->assertSame([], $this->calls, 'Starting queue RPC executed a step inline');
        $this->assertSame('logical-1', $started['run']['id']);
        $this->assertSame('queue', $started['metadata']['execution_driver']);
        $this->assertArrayNotHasKey('_queue_storage_id', $started['metadata']);
        $this->assertArrayNotHasKey('_rpc_authority', $started['metadata']);
        $this->assertTrue($this->worker->tick());
        $this->version = 2;
        $waiting = $this->app->invoke('flow.get', $params)['state'];
        $this->assertSame(1, $waiting['steps']['approve']['attempts']);
        $this->assertTrue($waiting['steps']['approve']['metadata']['deferred']);
        $this->assertSame($waiting, $this->app->invoke('flow.start', $params)['state']);
        $completion = $this->completion($params, ['total' => 100]);
        $completed = $this->app->invoke('flow.complete', $completion);
        $this->assertSame($completed, $this->app->invoke('flow.complete', $completion));
        $this->assertSame(['total' => 100], $completed['state']['steps']['approve']['output']);
        $this->app->invoke('flow.resume', [...$params, 'context' => ['source' => 'tampered']]);
        $this->assertTrue($this->worker->tick());
        $this->assertFalse($this->worker->tick());
        $finished = $this->app->invoke('flow.get', $params);
        $this->assertSame('completed', $finished['state']['status']);
        $this->assertSame(1, $finished['state']['run']['definition']['version']);
        $this->assertSame(['logical-1', 'orders', 'notify-v1', 1, 'original'], $this->calls[1]);
        $this->assertSame('pending', $this->reservation($params)->get('orders')->status->value);
        $this->assertSame($finished, $this->app->invoke('flow.resume', $params));
    }

    #[DataProvider('backends')]
    public function testScopeIsolationAndCancellationFenceOnlyTheTargetRun(string $backend): void
    {
        $this->open($backend);
        $a = $this->params(); $b = $this->params('two'); $c = $this->params('one', 'other-flow');
        $ids = [$this->start($a)['metadata']['execution_id'], $this->start($b)['metadata']['execution_id'], $this->start($c)['metadata']['execution_id']];
        $this->assertCount(3, array_unique($ids));
        $claims = $this->queue->claim(4, 60);
        $this->assertCount(3, $claims);
        $this->app->invoke('flow.cancel', $a);
        foreach ($claims as $claim) { $this->worker->handle($claim->claimId); }
        $this->assertSame('cancelled', $this->app->invoke('flow.get', $a)['state']['status']);
        $this->assertSame(1, $this->app->invoke('flow.get', $a)['state']['steps']['approve']['attempts']);
        $this->assertCount(2, $this->calls);
        $this->assertTrue($this->app->invoke('flow.get', $b)['state']['steps']['approve']['metadata']['deferred']);
        $this->app->invoke('flow.complete', $this->completion($b));
        $this->assertTrue($this->app->invoke('flow.get', $c)['state']['steps']['approve']['metadata']['deferred']);
        $this->assertSame('cancelled', $this->app->invoke('flow.cancel', $a)['state']['status']);
    }

    #[DataProvider('backends')]
    public function testResumePausesAndDeadlinesUseQueueState(string $backend): void
    {
        $this->open($backend);
        $this->mode = 'pending';
        $params = $this->params();
        $this->start($params); $this->worker->tick();
        $this->assertFalse($this->worker->tick());
        $this->app->invoke('flow.start', $params); // idempotent start must not resume a paused pass
        $this->assertFalse($this->worker->tick());
        $this->mode = 'deadline';
        $this->app->invoke('flow.resume', $params); $this->worker->tick();
        $this->now += 5;
        $this->queue->reconcile();
        $this->assertSame('completed', $this->app->invoke('flow.get', $params)['state']['steps']['approve']['status']);
        $this->assertSame(2, $this->app->invoke('flow.get', $params)['state']['steps']['approve']['attempts']);
    }

    public function testInitializationFailureCanRetryWithPinnedDefinitionAndContext(): void
    {
        $this->open('sqlite'); $params = $this->params();
        $this->pdo->exec("CREATE TRIGGER fail_start BEFORE INSERT ON workflow_queue_runs BEGIN SELECT RAISE(ABORT, 'unavailable'); END");
        try { $this->start($params); $this->fail('Expected queue storage failure'); } catch (\PDOException) {}
        $this->version = 2;
        try { $this->app->invoke('flow.get', $params); $this->fail('Returned routing snapshot'); }
        catch (RpcFailure $e) { $this->assertSame(-32009, $e->rpcCode); }
        $this->pdo->exec('DROP TRIGGER fail_start');
        $state = $this->app->invoke('flow.start', [...$params, 'context' => ['source' => 'different']])['state'];
        $this->assertSame(1, $state['run']['definition']['version']);
        $this->worker->tick();
        $this->assertSame('original', $this->calls[0][4]);
    }

    public function testQueueUnavailabilityCannotFallBackToProjection(): void
    {
        $this->open('sqlite'); $params = $this->params(); $this->start($params);
        $without = $this->application(queue: false);
        foreach (['flow.start', 'flow.get', 'flow.resume', 'flow.complete', 'flow.cancel'] as $method) {
            try { $without->invoke($method, $method === 'flow.complete' ? $this->completion($params) : $params); $this->fail('Fell back to projection'); }
            catch (RpcFailure $e) { $this->assertSame(-32602, $e->rpcCode); }
        }
        $this->assertSame('pending', $this->reservation($params)->get('orders')->status->value);
    }

    public function testLegacyQueueRoutesRequireMigrationAndDriverCannotSwitch(): void
    {
        $this->open('sqlite'); $params = $this->params();
        $this->reservation($params)->put('orders', (new FlowRun('logical-1', $this->definition('orders')))->initialState()->withMetadata(['execution_driver' => 'queue']));
        foreach (['flow.start', 'flow.get', 'flow.resume', 'flow.cancel'] as $method) {
            try { $this->app->invoke($method, $params); $this->fail('Migrated implicitly'); }
            catch (RpcFailure $e) { $this->assertSame(-32009, $e->rpcCode); }
        }
        $fresh = $this->params('fresh'); $this->start($fresh);
        $this->expectException(RpcFailure::class);
        $this->app->invoke('flow.start', [...$fresh, 'driver' => 'inline']);
    }

    public function testWrongAttemptsAndConflictingCallbacksDoNotChangeAuthority(): void
    {
        $this->open('sqlite'); $params = $this->params(); $this->start($params); $this->worker->tick();
        $completion = $this->completion($params);
        try { $this->app->invoke('flow.complete', [...$completion, 'attempt' => 2]); $this->fail('Stale attempt accepted'); }
        catch (\InvalidArgumentException) { $this->assertSame(1, $this->app->invoke('flow.get', $params)['state']['steps']['approve']['attempts']); }
        $this->app->invoke('flow.complete', $completion);
        $this->expectException(\InvalidArgumentException::class);
        $this->app->invoke('flow.complete', $this->completion($params, 'changed'));
    }

    #[DataProvider('backends')]
    public function testCaseDistinctSubjectsKeepTheirOwnContext(string $backend): void
    {
        $this->open($backend);
        foreach (['A', 'a'] as $subject) {
            $this->app->invoke('flow.start', [...$this->params($subject), 'driver' => 'queue', 'context' => ['source' => $subject]]);
        }
        $this->worker->tick(); $this->worker->tick();
        $sources = array_column($this->calls, 4); sort($sources);
        $this->assertSame(['A', 'a'], $sources);
        $this->app->invoke('flow.complete', $this->completion($this->params('A')));
        $this->assertTrue($this->app->invoke('flow.get', $this->params('a'))['state']['steps']['approve']['metadata']['deferred']);
    }

    public function testMismatchedRoutingScopeFailsClosed(): void
    {
        $this->open('sqlite'); $params = $this->params(); $this->start($params);
        $this->reservation($params)->mutate('orders', fn ($state) => $state->withMetadata(['execution_id' => 'different-scope']));
        $this->expectException(RpcFailure::class);
        $this->app->invoke('flow.get', $params);
    }

    public function testMysqlUpgradeRejectsOldCollisionsThenAllowsDistinctSubjects(): void
    {
        $this->open('mysql');
        $this->pdo->exec('ALTER TABLE workflow_rpc_states CONVERT TO CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci');
        $this->app->invoke('flow.start', [...$this->params('A'), 'driver' => 'queue', 'context' => ['source' => 'A']]);
        try {
            $this->app->invoke('flow.start', [...$this->params('a'), 'driver' => 'queue', 'context' => ['source' => 'a']]);
            $this->fail('Old case-insensitive registry reused another subject context');
        } catch (RpcFailure $e) { $this->assertSame(-32009, $e->rpcCode); }
        $this->pdo->exec(file_get_contents(dirname(__DIR__, 2) . '/schema/mysql-queue-rpc-upgrade.sql'));
        $this->app->invoke('flow.start', [...$this->params('a'), 'driver' => 'queue', 'context' => ['source' => 'a']]);
        $this->worker->tick(); $this->worker->tick();
        $sources = array_column($this->calls, 4); sort($sources);
        $this->assertSame(['A', 'a'], $sources);
    }

    public function testQueueOnlyHostCanRepeatStartWithoutSupplyingDriverAgain(): void
    {
        $this->open('sqlite');
        $app = new FlowRpcApplication($this->definitions, $this->stores, new FlowExecutionDispatcher([]), queue: new QueueRpcRuntime($this->queue));
        $params = $this->params();
        $first = $app->invoke('flow.start', [...$params, 'driver' => 'queue', 'context' => ['source' => 'original']]);
        $this->assertSame($first, $app->invoke('flow.start', $params));
        $this->worker->tick();
        $this->assertSame(1, $app->invoke('flow.start', $params)['state']['steps']['approve']['attempts']);
    }

    private function drop(): void
    {
        foreach (['workflow_queue_outbox', 'workflow_queue_claims', 'workflow_queue_runs', 'workflow_queue_lock'] as $table) { $this->pdo->exec('DROP TABLE IF EXISTS ' . $table); }
    }
    protected function tearDown(): void { if (isset($this->pdo)) { $this->drop(); } }
}
