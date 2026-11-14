<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver\Tests;

use Webong\WorkFlow\Contracts\ForgettableFlowStateStore;
use Webong\WorkFlow\Enums\FlowStepStatus;
use Webong\WorkFlow\Services\RunScopedFlowStateStore;
use Webong\WorkFlow\ValueObjects\FlowDefinition;
use Webong\WorkFlow\ValueObjects\FlowRun;
use Webong\WorkFlow\ValueObjects\FlowState;
use Webong\WorkFlow\ValueObjects\FlowStepDefinition;
use Webong\WorkFlow\ValueObjects\StepOutput;
use Webong\WorkFlow\ValueObjects\StepState;

trait RunStoreAssertions
{
    private function assertRunIsolation(ForgettableFlowStateStore $storage): void
    {
        $definition = new FlowDefinition('run-isolation', [new FlowStepDefinition('authorize', 'Authorize')], metadata: ['z' => 'last', 'a' => 'first', 'ratio' => 1.0], version: 7);
        $first = new RunScopedFlowStateStore($storage, 'first');
        $second = new RunScopedFlowStateStore($storage, str_repeat('second', 100));
        $first->put($definition->key, (new FlowRun('first', $definition))->initialState());
        $second->put($definition->key, (new FlowRun($second->runId, $definition))->initialState());
        $first->mutate($definition->key, static fn (?FlowState $state): FlowState => $state->withMetadata(['one' => true]));

        self::assertTrue($first->get($definition->key)->metadata['one']);
        self::assertArrayNotHasKey('one', $second->get($definition->key)->metadata);
        self::assertSame(7, $first->get($definition->key)->run->definition->version);
        $first->get($definition->key)->run->assertDefinition($definition);

        $first->forget($definition->key);
        self::assertNull($first->get($definition->key));
        self::assertNotNull($second->get($definition->key));
        $second->forget($definition->key);
    }

    /** First-class outputs must survive every real storage engine. */
    private function assertStructuredOutputRoundTrip(ForgettableFlowStateStore $storage): void
    {
        $definition = new FlowDefinition('output-roundtrip', [new FlowStepDefinition('emit', 'Emit')]);

        $store = new RunScopedFlowStateStore($storage, 'output-run');
        $store->put($definition->key, (new FlowRun('output-run', $definition))->initialState()->withStep('emit', new StepState(
            FlowStepStatus::COMPLETED,
            message: 'computed',
            attempts: 1,
            output: StepOutput::from(['total' => 100, 'items' => [1, 2], 'nested' => ['ok' => true]]),
        )));
        $reloaded = $store->get($definition->key);

        self::assertNotNull($reloaded);
        $step = $reloaded->steps['emit'] ?? null;
        self::assertNotNull($step);
        self::assertNotNull($step->output, 'Structured output must survive the store round trip');
        // Object key order is not domain data (JSON columns may reorder it).
        self::assertSame(100, $step->output->value['total']);
        self::assertSame([1, 2], $step->output->value['items']);
        self::assertSame(['ok' => true], $step->output->value['nested']);

        // A present JSON null stays present through the same path.
        $nullStore = new RunScopedFlowStateStore($storage, 'output-null-run');
        $nullStore->put($definition->key, (new FlowRun('output-null-run', $definition))->initialState()->withStep('emit', new StepState(
            FlowStepStatus::COMPLETED,
            output: StepOutput::null(),
        )));
        $reloaded = $nullStore->get($definition->key);
        self::assertNotNull($reloaded->steps['emit']->output ?? null);
        self::assertNull($reloaded->steps['emit']->output?->value);

        // An output-less step keeps having no output.
        $absentStore = new RunScopedFlowStateStore($storage, 'output-absent-run');
        $absentStore->put($definition->key, (new FlowRun('output-absent-run', $definition))->initialState()->withStep('emit', new StepState(
            FlowStepStatus::COMPLETED,
        )));
        $reloaded = $absentStore->get($definition->key);
        self::assertNull($reloaded->steps['emit']->output);
        self::assertArrayNotHasKey('output', $reloaded->steps['emit']->toArray());

        $store->forget($definition->key);
        $nullStore->forget($definition->key);
        $absentStore->forget($definition->key);
    }
}
