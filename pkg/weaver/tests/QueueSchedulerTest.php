<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver\Tests;

use PDO;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use RuntimeException;
use Webong\Gateway\Weaver\QueueOutboxDispatcher;
use Webong\Gateway\Weaver\QueueScheduler;
use Webong\Gateway\Weaver\QueueWorker;
use Webong\WorkFlow\Contracts\FlowStepExecutor;
use Webong\WorkFlow\Enums\FlowStatus;
use Webong\WorkFlow\Enums\FlowStepStatus;
use Webong\WorkFlow\Enums\FlowInputSource;
use Webong\WorkFlow\ValueObjects\FlowDeferredCompletion;
use Webong\WorkFlow\ValueObjects\FlowDefinition;
use Webong\WorkFlow\ValueObjects\FlowInputBinding;
use Webong\WorkFlow\ValueObjects\FlowRetryPolicy;
use Webong\WorkFlow\ValueObjects\FlowRun;
use Webong\WorkFlow\ValueObjects\FlowStepClaim;
use Webong\WorkFlow\ValueObjects\FlowStepDefinition;
use Webong\WorkFlow\ValueObjects\FlowStepInvocation;
use Webong\WorkFlow\ValueObjects\StepOutput;
use Webong\WorkFlow\ValueObjects\StepResult;

final class QueueSchedulerTest extends TestCase
{
    private PDO $pdo;
    private QueueScheduler $queue;
    private int $now;
    private string $dsn;
    private string $user = '';
    private string $password = '';
    private string $directory;

    public static function backends(): array
    {
        return [['sqlite'], ['postgres'], ['mysql']];
    }

    private function open(string $backend, int $capacity = 2): void
    {
        $this->directory = sys_get_temp_dir() . '/queue-' . bin2hex(random_bytes(8));
        mkdir($this->directory);
        $this->now = time();
        if ($backend === 'sqlite') {
            $this->dsn = 'sqlite:' . $this->directory . '/state.sqlite';
        } else {
            $key = $backend === 'postgres' ? 'POSTGRES' : 'MYSQL';
            $this->dsn = getenv('WORK_FLOW_' . $key . '_DSN') ?: '';
            if ($this->dsn === '') {
                $this->fail($backend . ' DSN not configured');
            }
            $this->user = getenv('WORK_FLOW_' . $key . '_USER') ?: '';
            $this->password = getenv('WORK_FLOW_' . $key . '_PASSWORD') ?: '';
        }
        $this->pdo = new PDO($this->dsn, $this->user, $this->password, [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);
        $this->drop();
        QueueScheduler::install($this->pdo, $capacity);
        $this->queue = new QueueScheduler($this->pdo, fn (): int => $this->now);
    }

    private function drop(): void
    {
        foreach (['workflow_queue_outbox', 'workflow_queue_claims', 'workflow_queue_runs', 'workflow_queue_lock', 'queue_effects'] as $table) {
            $this->pdo->exec('DROP TABLE IF EXISTS ' . $table);
        }
    }

    protected function tearDown(): void
    {
        if (isset($this->pdo)) { $this->drop(); }
        if (isset($this->directory)) {
            foreach (glob($this->directory . '/*') ?: [] as $path) { unlink($path); }
            rmdir($this->directory);
        }
    }

    private function start(array $steps, array $context = [], string $id = 'run'): void
    {
        $this->queue->start(new FlowRun($id, new FlowDefinition('orders', $steps)), $context);
    }

    private function step(string $id = 'a', bool $retry = false): FlowStepDefinition
    {
        return new FlowStepDefinition($id, $id, retryPolicy: $retry ? new FlowRetryPolicy(maxAttempts: 3, backoffSeconds: 10) : null);
    }

    private function events(): array
    {
        $events = $this->queue->reserveEvents();
        foreach ($events as $event) { $this->queue->acknowledgeEvent($event['id'], $event['token']); }
        return $events;
    }

    #[DataProvider('backends')]
    public function testFanOutJoinsLatestStateAndPreservesOutputPresence(string $backend): void
    {
        $this->open($backend);
        $this->start([$this->step('a'), $this->step('b'), new FlowStepDefinition('c', 'c', dependsOn: ['a', 'b'], input: [
            'total' => new FlowInputBinding(FlowInputSource::OUTPUT, '/total', 'b'),
        ])], ['customer' => 42]);
        [$a, $b] = $this->queue->claim(10, 60);
        $this->assertSame(['customer' => 42], $a->context);
        $this->assertSame([], $this->queue->claim(10, 60));
        $this->queue->complete($b, StepResult::completed(output: StepOutput::from(['total' => 100])));
        $this->assertSame([], $this->queue->claim(10, 60));
        $this->queue->complete($a, StepResult::completed());
        $this->assertNull($this->queue->get('run')->steps['a']->output);
        $this->assertSame(['total' => 100], $this->queue->get('run')->steps['b']->output->value);
        [$c] = $this->queue->claim(1, 60);
        $this->assertSame(['total' => 100], $c->input);
        $this->queue->complete($c, StepResult::completed(output: StepOutput::null()));
        $this->assertNotNull($this->queue->get('run')->steps['c']->output);
        $this->assertSame(FlowStatus::COMPLETED, $this->queue->get('run')->status);
        $this->assertSame([], $this->queue->claim(10, 60));
    }

    #[DataProvider('backends')]
    public function testCapacityIsSharedAcrossConnectionsAndRuns(string $backend): void
    {
        $this->open($backend, 1);
        $this->start([$this->step()], id: 'one');
        $this->start([$this->step()], id: 'two');
        $other = new QueueScheduler(new PDO($this->dsn, $this->user, $this->password));
        [$first] = $this->queue->claim(10, 60);
        $this->assertSame([], $other->claim(10, 60));
        $this->queue->complete($first, StepResult::completed());
        $this->assertCount(1, $other->claim(10, 60));
    }

    #[DataProvider('backends')]
    public function testBoundedRetriesRespectBackoffAndAttemptIdentity(string $backend): void
    {
        $this->open($backend);
        $this->start([$this->step(retry: true)]);
        for ($attempt = 1; $attempt <= 3; ++$attempt) {
            [$claim] = $this->queue->claim(1, 60);
            $this->assertSame($attempt, $claim->metadata['attempt']);
            $this->queue->fail($claim, new RuntimeException('private token=secret'));
            $state = $this->queue->get('run');
            $this->assertStringNotContainsString('secret', json_encode($state->toArray()));
            $this->assertSame($attempt, $state->steps['a']->attempts);
            $this->assertSame([], $this->queue->claim(1, 60));
            if ($attempt < 3) {
                $this->assertSame($this->now + 10, $this->queue->nextWakeup());
                $this->now += 10;
            }
        }
        $this->assertSame(FlowStatus::BLOCKED, $this->queue->get('run')->status);
        $this->assertNull($this->queue->nextWakeup());
    }

    #[DataProvider('backends')]
    public function testExpiredAttemptCannotCompleteAfterRecovery(string $backend): void
    {
        $this->open($backend);
        $this->start([$this->step(retry: true)]);
        [$old] = $this->queue->claim(1, 5);
        $this->now += 5;
        $this->assertNull($this->queue->beginExecution($old->claimId));
        $this->assertSame(1, $this->queue->reconcile());
        $this->now += 10;
        [$new] = $this->queue->claim(1, 60);
        $this->assertSame(2, $new->metadata['attempt']);
        $this->assertSame($old->metadata['idempotency_key'], $new->metadata['idempotency_key']);
        try { $this->queue->complete($old, StepResult::completed()); $this->fail('Stale claim accepted'); }
        catch (RuntimeException $e) { $this->assertStringContainsString('lease lost', $e->getMessage()); }
        $this->queue->complete($new, StepResult::completed());
        $this->assertSame(FlowStatus::COMPLETED, $this->queue->get('run')->status);
    }

    #[DataProvider('backends')]
    public function testUnknownNonIdempotentOutcomeRequiresReconciliation(string $backend): void
    {
        $this->open($backend);
        $this->start([new FlowStepDefinition('a', 'a', retryPolicy: new FlowRetryPolicy(idempotent: false))]);
        $this->queue->claim(1, 1);
        ++$this->now;
        $this->assertSame(1, $this->queue->reconcile());
        $this->assertSame([], $this->queue->claim(1, 60));
        $this->assertSame('claim_lost', $this->queue->get('run')->steps['a']->metadata['error_code']);
        $this->assertSame(FlowStatus::BLOCKED, $this->queue->get('run')->status);
    }

    #[DataProvider('backends')]
    public function testRenewalAndReleaseAreFenced(string $backend): void
    {
        $this->open($backend);
        $this->start([$this->step(retry: true)]);
        [$claim] = $this->queue->claim(1, 5);
        $this->now += 4;
        $this->queue->renew($claim->claimId, 30);
        $this->now += 2;
        $this->assertSame(0, $this->queue->reconcile());
        $this->queue->release($claim);
        $this->assertSame([], $this->queue->claim(1, 60));
        $this->now += 10;
        $this->assertCount(1, $this->queue->claim(1, 60));
        $this->expectException(RuntimeException::class);
        $this->queue->renew($claim, 10);
    }

    #[DataProvider('backends')]
    public function testDuplicateResultsAreIdempotentAndConflictsRejected(string $backend): void
    {
        $this->open($backend);
        $this->start([$this->step()]);
        [$claim] = $this->queue->claim(1, 60);
        $result = StepResult::completed(output: StepOutput::from(100));
        $this->queue->complete($claim, $result);
        $this->queue->complete($claim, $result);
        $this->assertSame(1, count(array_filter($this->events(), fn ($e) => $e['type'] === 'step.completed')));
        $this->expectException(RuntimeException::class);
        $this->queue->complete($claim, StepResult::completed(output: StepOutput::from(200)));
    }

    #[DataProvider('backends')]
    public function testCancellationFencesInflightEffectsAndIsIdempotent(string $backend): void
    {
        $this->open($backend);
        $this->start([$this->step(), $this->step('b')]);
        [$claim] = $this->queue->claim(2, 60);
        $this->queue->cancelRun('run');
        $this->queue->cancelRun('run');
        $this->assertNull($this->queue->beginExecution($claim->claimId));
        $this->assertSame([], $this->queue->claim(2, 60));
        $this->assertSame(FlowStatus::CANCELLED, $this->queue->get('run')->status);
        $this->expectException(RuntimeException::class);
        $this->queue->complete($claim, StepResult::completed());
    }

    #[DataProvider('backends')]
    public function testDeferredCallbacksAndDeadlinesAreDurable(string $backend): void
    {
        $this->open($backend);
        $this->start([$this->step(), new FlowStepDefinition('b', 'b', dependsOn: ['a'])]);
        [$claim] = $this->queue->claim(1, 60);
        $this->queue->complete($claim, StepResult::deferred(deadlineAt: $this->now + 10));
        $this->assertSame([], $this->queue->claim(1, 60));
        $callback = new FlowDeferredCompletion('orders', 'a', 'event1', StepResult::completed(), 'run', 1);
        $this->queue->completeDeferred($callback);
        $this->queue->completeDeferred($callback);
        [$b] = $this->queue->claim(1, 60);
        $this->queue->complete($b, StepResult::deferred(deadlineAt: $this->now + 10));
        $this->now += 10;
        $this->queue->reconcile();
        $this->assertSame(FlowStatus::BLOCKED, $this->queue->get('run')->status);
    }

    #[DataProvider('backends')]
    public function testOutboxSurvivesPublisherCrashAndRejectsOldAcknowledgement(string $backend): void
    {
        $this->open($backend);
        $this->start([$this->step()]);
        $this->events();
        [$claim] = $this->queue->claim(1, 60);
        [$first] = $this->queue->reserveEvents(1, 5);
        $this->assertSame('work.ready', $first['type']);
        $this->assertSame($claim->claimId, $first['payload']['claim_id']);
        // Publish succeeds, process dies before acknowledgement. Replay keeps the event ID.
        $this->now += 5;
        [$replay] = $this->queue->reserveEvents(1, 5);
        $this->assertSame($first['id'], $replay['id']);
        $this->assertNotSame($first['token'], $replay['token']);
        try { $this->queue->acknowledgeEvent($first['id'], $first['token']); $this->fail('Old token accepted'); }
        catch (RuntimeException) { $this->assertTrue(true); }
        $this->queue->acknowledgeEvent($replay['id'], $replay['token']);
        $this->assertSame([], $this->queue->reserveEvents());
    }

    #[DataProvider('backends')]
    public function testPublisherRunsOutsideLockAndFailureBacksOff(string $backend): void
    {
        $this->open($backend);
        $this->start([$this->step()]);
        $calls = 0;
        $publisher = new QueueOutboxDispatcher($this->queue, function () use (&$calls): void {
            $this->assertFalse($this->pdo->inTransaction());
            ++$calls;
            throw new RuntimeException('broker offline');
        });
        try { $publisher->dispatch(); $this->fail('Publish should fail'); } catch (RuntimeException) {}
        $this->assertSame(1, $calls);
        $this->assertSame([], $this->queue->reserveEvents());
        $this->now += 5;
        $this->assertCount(1, $this->queue->reserveEvents());
    }

    #[DataProvider('backends')]
    public function testWorkerUsesSharedExecutionAndDeduplicatesBrokerDelivery(string $backend): void
    {
        $this->open($backend);
        $this->start([$this->step()]);
        [$claim] = $this->queue->claim(1, 60);
        $executor = new class($this->pdo) implements FlowStepExecutor {
            public int $calls = 0;
            public function __construct(private PDO $pdo) {}
            public function supports(FlowStepDefinition $step): bool { return true; }
            public function execute(FlowStepInvocation $invocation): StepResult {
                TestCase::assertFalse($this->pdo->inTransaction());
                TestCase::assertSame(1, $invocation->attempt);
                TestCase::assertNotEmpty($invocation->context->all()['_queue']['idempotency_key']);
                ++$this->calls;
                return StepResult::deferred('approval');
            }
        };
        $worker = new QueueWorker($this->queue, [$executor]);
        $this->assertTrue($worker->handle($claim->claimId));
        $this->assertFalse($worker->handle($claim->claimId));
        $this->assertSame(1, $executor->calls);
        $this->assertSame(true, $this->queue->get('run')->steps['a']->metadata['deferred']);
        $this->assertSame([], $this->queue->claim(1, 60));
    }

    public function testClaimStateAndPublicationRollBackTogether(): void
    {
        $this->open('sqlite');
        $this->start([$this->step()]);
        $this->events();
        $this->pdo->exec("CREATE TRIGGER reject_outbox BEFORE INSERT ON workflow_queue_outbox BEGIN SELECT RAISE(ABORT, 'disk failure'); END");
        try { $this->queue->claim(1, 60); $this->fail('Expected storage failure'); } catch (\PDOException) {}
        $this->assertSame(0, $this->queue->get('run')->steps['a']->attempts);
        $this->assertSame([], $this->queue->reserveEvents());
        $this->pdo->exec('DROP TRIGGER reject_outbox');
        $this->assertCount(1, $this->queue->claim(1, 60));
    }

    public function testCompletionStateAndOutboxRollBackTogether(): void
    {
        $this->open('sqlite');
        $this->start([$this->step()]);
        [$claim] = $this->queue->claim(1, 60);
        $this->events();
        $this->pdo->exec("CREATE TRIGGER reject_outbox BEFORE INSERT ON workflow_queue_outbox BEGIN SELECT RAISE(ABORT, 'disk failure'); END");
        try { $this->queue->complete($claim, StepResult::completed()); $this->fail('Expected storage failure'); } catch (\PDOException) {}
        $this->assertSame(FlowStepStatus::RUNNING, $this->queue->get('run')->steps['a']->status);
        $this->pdo->exec('DROP TRIGGER reject_outbox');
        $this->queue->complete($claim, StepResult::completed());
        $this->assertSame(FlowStatus::COMPLETED, $this->queue->get('run')->status);
    }

    #[DataProvider('backends')]
    public function testCompetingProcessesClaimOneStepOnlyOnce(string $backend): void
    {
        $this->open($backend);
        $this->start([$this->step()]);
        $children = [$this->process('claim', 'one'), $this->process('claim', 'two')];
        touch($this->directory . '/go');
        $results = array_map(fn ($child) => $this->finishProcess($child), $children);
        $this->assertSame(1, count(array_filter($results, fn ($value) => $value !== 'none')));
    }

    #[DataProvider('backends')]
    public function testSeparateWorkersExecuteIndependentStepsConcurrentlyBeforeJoin(string $backend): void
    {
        $this->open($backend);
        $this->start([$this->step(), $this->step('b'), new FlowStepDefinition('c', 'c', dependsOn: ['a', 'b'])]);
        $children = [$this->process('work', 'one'), $this->process('work', 'two')];
        touch($this->directory . '/go');
        foreach ($children as $child) { $this->assertSame('done', $this->finishProcess($child)); }
        $state = $this->queue->get('run');
        $this->assertSame(FlowStepStatus::COMPLETED, $state->steps['a']->status);
        $this->assertSame(FlowStepStatus::COMPLETED, $state->steps['b']->status);
        [$join] = $this->queue->claim(1, 60);
        $this->assertSame('c', $join->step->id);
        $this->queue->complete($join, StepResult::completed());
        $this->assertSame(FlowStatus::COMPLETED, $this->queue->get('run')->status);
    }

    #[DataProvider('backends')]
    public function testRestartAfterEffectUsesStableProviderIdempotencyKey(string $backend): void
    {
        $this->open($backend);
        $this->start([$this->step(retry: true)]);
        $this->pdo->exec('CREATE TABLE queue_effects (effect_key VARCHAR(64) PRIMARY KEY)');
        $child = $this->process('crash', 'one');
        touch($this->directory . '/go');
        $first = FlowStepClaim::fromArray(json_decode($this->finishProcess($child), true, flags: JSON_THROW_ON_ERROR));
        $key = $first->metadata['idempotency_key'];
        // Worker dies after provider acceptance but before recording its result.
        $this->now = (int) $first->metadata['lease_expires_at'];
        $restarted = new QueueScheduler(new PDO($this->dsn, $this->user, $this->password), fn (): int => $this->now);
        $restarted->reconcile();
        $this->now += 10;
        [$second] = $restarted->claim(1, 60);
        $this->assertSame($key, $second->metadata['idempotency_key']);
        // Provider reports that the stable key already exists, so no second effect is performed.
        $stmt = $this->pdo->prepare('SELECT COUNT(*) FROM queue_effects WHERE effect_key = ?');
        $stmt->execute([$second->metadata['idempotency_key']]);
        $this->assertSame(1, (int) $stmt->fetchColumn());
        $stmt->closeCursor();
        $restarted->complete($second, StepResult::completed());
        $this->assertSame(FlowStatus::COMPLETED, $restarted->get('run')->status);
    }

    #[DataProvider('backends')]
    public function testFailureDoesNotOverwriteSiblingCompletion(string $backend): void
    {
        $this->open($backend);
        $this->start([$this->step(), $this->step('b')]);
        [$a, $b] = $this->queue->claim(2, 60);
        $this->queue->complete($b, StepResult::completed(output: StepOutput::from('saved')));
        $this->queue->fail($a, new RuntimeException('private'));
        $this->assertSame('saved', $this->queue->get('run')->steps['b']->output->value);
    }

    #[DataProvider('backends')]
    public function testStartIsImmutableAndIdempotent(string $backend): void
    {
        $this->open($backend);
        $this->start([$this->step()], ['customer' => 1]);
        [$claim] = $this->queue->claim(1, 60);
        $this->start([$this->step()], ['customer' => 1]);
        $this->assertSame(1, $this->queue->get('run')->steps['a']->attempts);
        $this->expectException(\InvalidArgumentException::class);
        $this->start([$this->step()], ['customer' => 2]);
    }

    public function testResultSerializationPreservesScalarsAndAbsence(): void
    {
        foreach ([false, 1, 'hello', null, ['one' => 1]] as $value) {
            $result = StepResult::fromArray(StepResult::completed(output: StepOutput::from($value))->toArray());
            $this->assertNotNull($result->output);
            $this->assertSame($value, $result->output->value);
        }
        $this->assertNull(StepResult::fromArray(StepResult::completed()->toArray())->output);
        $this->assertNotSame(StepResult::completed()->toArray(), StepResult::completed(output: StepOutput::null())->toArray());
    }

    public function testMissingBindingsFailBeforeExecutionWithoutBlockingOtherRuns(): void
    {
        $this->open('sqlite');
        $this->start([new FlowStepDefinition('a', 'a', input: ['required' => FlowInputBinding::context('/missing')])]);
        $this->start([$this->step()], id: 'other');
        [$claim] = $this->queue->claim(2, 60);
        $this->assertSame('other', $claim->run->id);
        $this->assertSame('input_resolution_failed', $this->queue->get('run')->steps['a']->metadata['error_code']);
    }

    #[DataProvider('backends')]
    public function testCompetingBrokerConsumersExecuteOneAttemptOnce(string $backend): void
    {
        $this->open($backend);
        $this->start([$this->step()]);
        [$claim] = $this->queue->claim(1, 60);
        file_put_contents($this->directory . '/claim-id', $claim->claimId);
        $children = [$this->process('deliver', 'one'), $this->process('deliver', 'two')];
        touch($this->directory . '/go');
        $results = array_map(fn ($child) => $this->finishProcess($child), $children);
        sort($results);
        $this->assertSame(['done', 'none'], $results);
        $this->assertSame("effect\n", file_get_contents($this->directory . '/effects'));
    }

    public function testWorkerSchemaHookFailsBeforeEffects(): void
    {
        $this->open('sqlite');
        $this->start([$this->step()]);
        $schemas = new class implements \Webong\WorkFlow\Contracts\FlowStepSchemaValidator {
            public function validateInput(FlowStepDefinition $step, \Webong\WorkFlow\ValueObjects\FlowStepInput $input): array { return ['missing required field']; }
            public function validateOutput(FlowStepDefinition $step, StepOutput $output): array { return []; }
        };
        $executor = new class implements FlowStepExecutor {
            public int $calls = 0;
            public function supports(FlowStepDefinition $step): bool { return true; }
            public function execute(FlowStepInvocation $invocation): StepResult { ++$this->calls; return StepResult::completed(); }
        };
        $worker = new QueueWorker($this->queue, [$executor], new \Webong\WorkFlow\Services\FlowStepExecution(schemas: $schemas));
        $worker->tick();
        $this->assertSame(0, $executor->calls);
        $this->assertSame('input_schema_violation', $this->queue->get('run')->steps['a']->metadata['error_code']);
    }

    public function testDeferredFailureWithoutPolicyCannotRetryForever(): void
    {
        $this->open('sqlite');
        $this->start([$this->step()]);
        [$claim] = $this->queue->claim(1, 60);
        $this->queue->complete($claim, StepResult::deferred());
        $this->queue->completeDeferred(new FlowDeferredCompletion('orders', 'a', 'event', StepResult::failed('failed', retriable: true), 'run', 1));
        $this->assertSame([], $this->queue->claim(1, 60));
        $this->assertSame(FlowStatus::BLOCKED, $this->queue->get('run')->status);
    }

    public function testPlainPendingPausesUntilExplicitResume(): void
    {
        $this->open('sqlite');
        $this->start([$this->step()]);
        [$claim] = $this->queue->claim(1, 60);
        $this->queue->complete($claim, StepResult::pending());
        $this->assertSame([], $this->queue->claim(1, 60));
        $this->assertNull($this->queue->nextWakeup());
        $this->queue->resumeRun('run');
        [$next] = $this->queue->claim(1, 60);
        $this->assertSame(2, $next->metadata['attempt']);
    }

    public function testInvalidContextIsRejectedBeforePersistingARun(): void
    {
        $this->open('sqlite');
        try { $this->start([$this->step()], ['unnamed']); $this->fail('Invalid context accepted'); }
        catch (\InvalidArgumentException) { $this->assertNull($this->queue->get('run')); }
        $this->assertSame([], $this->queue->reserveEvents());
    }

    private function process(string $mode, string $name): array
    {
        $pipes = [];
        $process = proc_open([PHP_BINARY, __DIR__ . '/fixtures/queue-process.php', $this->dsn, $this->user, $this->password, $this->directory, $mode, $name], [0 => ['pipe', 'r'], 1 => ['pipe', 'w'], 2 => ['pipe', 'w']], $pipes);
        $this->assertIsResource($process);
        fclose($pipes[0]);
        return [$process, $pipes];
    }

    private function finishProcess(array $child): string
    {
        [$process, $pipes] = $child;
        $output = stream_get_contents($pipes[1]);
        $error = stream_get_contents($pipes[2]);
        fclose($pipes[1]); fclose($pipes[2]);
        $this->assertSame(0, proc_close($process), $error);
        return trim($output);
    }
}
