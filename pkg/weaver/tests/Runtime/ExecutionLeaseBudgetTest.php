<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver\Tests\Runtime;

use PDO;
use PHPUnit\Framework\TestCase;
use Webong\Gateway\Weaver\{QueueScheduler,QueueWorker};
use Webong\WorkFlow\Contracts\FlowStepExecutor;
use Webong\WorkFlow\ValueObjects\{FlowDefinition,FlowRun,FlowStepDefinition,FlowStepInvocation,StepResult};

final class ExecutionLeaseBudgetTest extends TestCase
{
    public function testExecutionExtendsAStillValidNearExpiryClaimBeforeItsEffect(): void
    {
        $connection=new PDO('sqlite::memory:');QueueScheduler::install($connection);
        $clock=(object)['now'=>1000,'effects'=>0];$queue=new QueueScheduler($connection,fn()=>$clock->now);
        $run=new FlowRun('slow-provider',new FlowDefinition('effect',[new FlowStepDefinition('send','Send')]));
        $queue->start($run);$claim=$queue->claim(1,2)[0];$clock->now++;
        $executor=new class($clock,$queue) implements FlowStepExecutor {
            public function __construct(private object $clock,private QueueScheduler $queue){}
            public function supports(FlowStepDefinition $step):bool{return true;}
            public function execute(FlowStepInvocation $invocation):StepResult{
                $this->clock->effects++;$this->clock->now+=3;
                TestCase::assertSame(0,$this->queue->reconcile(),'Live execution expired during its bounded effect');
                return StepResult::completed();
            }
        };
        $worker=new QueueWorker($queue,[$executor],executionLeaseSeconds:30);
        self::assertTrue($worker->handle($claim->claimId));
        self::assertSame('completed',$queue->get('slow-provider')->status->value);
        self::assertSame(1,$clock->effects);self::assertFalse($worker->handle($claim->claimId));
    }

    public function testExecutionBudgetCannotResurrectAnExpiredClaim(): void
    {
        $connection=new PDO('sqlite::memory:');QueueScheduler::install($connection);
        $clock=(object)['now'=>1000];$queue=new QueueScheduler($connection,fn()=>$clock->now);
        $queue->start(new FlowRun('expired',new FlowDefinition('effect',[new FlowStepDefinition('send','Send')])));
        $claim=$queue->claim(1,1)[0];$clock->now+=2;
        self::assertNull($queue->beginExecution($claim->claimId,30));
    }
}
