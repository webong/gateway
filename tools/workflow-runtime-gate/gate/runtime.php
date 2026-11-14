<?php
// Trusted feasibility fixture. No caller-selected PHP, paths, commands or classes.
use Webong\WorkFlow\App\{QueueScheduler,PdoSqliteFlowStateStoreFactory};
use Webong\WorkFlow\Contracts\{FlowDefinitionProvider,FlowStepExecutor};
use Webong\WorkFlow\Services\DefaultFlowStateSerializer;
use Webong\WorkFlow\ValueObjects\{FlowDefinition,FlowStepDefinition,FlowStepInvocation,FlowRetryPolicy,StepResult};
require_once dirname(__DIR__).'/app/vendor/autoload.php';
$connection = new PDO('sqlite:/state/workflow.sqlite', null, null, [PDO::ATTR_ERRMODE=>PDO::ERRMODE_EXCEPTION]);
$connection->exec('PRAGMA busy_timeout = 5000');
$definitions = new class implements FlowDefinitionProvider {
 public function definition(string $flowKey): FlowDefinition {
  if (!in_array($flowKey,['gate_order','gate_no_replay'],true)) throw new InvalidArgumentException('Unknown flow');
  return new FlowDefinition($flowKey, [new FlowStepDefinition('approve','Approve',retriable:$flowKey==='gate_order',retryPolicy:$flowKey==='gate_order'?new FlowRetryPolicy(maxAttempts:2,idempotent:true):null),new FlowStepDefinition('fulfil','Fulfil',dependsOn:['approve'])]);
 }
};
$executor = new class implements FlowStepExecutor {
 public function supports(FlowStepDefinition $step): bool {return in_array($step->id,['approve','fulfil'],true);}
 public function execute(FlowStepInvocation $invocation): StepResult {return $invocation->step->id==='approve'?StepResult::deferred('Await approval'):StepResult::completed('Fulfilled');}
};
return ['connection'=>$connection,'definitions'=>$definitions,'stores'=>new PdoSqliteFlowStateStoreFactory($connection,new DefaultFlowStateSerializer()),'executors'=>[$executor],'queue'=>new QueueScheduler($connection)];
