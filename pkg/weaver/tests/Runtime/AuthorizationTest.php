<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver\Tests\Runtime;

use PDO;
use PHPUnit\Framework\TestCase;
use Webong\Gateway\Weaver\{AuthorizedRpcApplication,FlowRpcApplication,OperationalSchema,PdoSqliteFlowStateStoreFactory,QueueRpcRuntime,QueueScheduler,RpcFailure,RunGrant};
use Webong\WorkFlow\Contracts\FlowDefinitionProvider;
use Webong\WorkFlow\Services\{DefaultFlowStateSerializer,FlowExecutionDispatcher};
use Webong\WorkFlow\ValueObjects\{FlowDefinition,FlowStepDefinition};

final class AuthorizationTest extends TestCase
{
    private PDO $connection;
    private AuthorizedRpcApplication $application;
    private const PARAMS = ['flow_key'=>'order','run_id'=>'same-run','subject'=>['type'=>'order','id'=>'same-subject']];

    protected function setUp(): void
    {
        $this->connection = new PDO('sqlite::memory:', options:[PDO::ATTR_ERRMODE=>PDO::ERRMODE_EXCEPTION]);
        OperationalSchema::install($this->connection);
        $definitions = new class implements FlowDefinitionProvider {
            public function definition(string $flowKey): FlowDefinition { return new FlowDefinition($flowKey,[new FlowStepDefinition('approve','Approve')],version:3); }
        };
        $app = new FlowRpcApplication($definitions,new PdoSqliteFlowStateStoreFactory($this->connection,new DefaultFlowStateSerializer()),
            new FlowExecutionDispatcher([]),queue:new QueueRpcRuntime(new QueueScheduler($this->connection)));
        $this->application = new AuthorizedRpcApplication($app,[
            new RunGrant('account-a','order','order','same-subject',['flow.start','flow.get','flow.cancel']),
            new RunGrant('account-b','order','order','same-subject',['flow.start','flow.get']),
        ]);
    }

    public function testAccountOwnershipDoesNotGrantRunControlOrAnotherScope(): void
    {
        foreach ([
            ['ungranted','flow.start',self::PARAMS],
            ['account-a','flow.complete',self::PARAMS],
            ['account-a','flow.start',[...self::PARAMS,'subject'=>['type'=>'order','id'=>'other']]],
            ['account-a','flow.start',[...self::PARAMS,'flow_key'=>'other']],
            ['account-a','flow.start',[...self::PARAMS,'account'=>'account-b']],
            ['account-a','flow.start',[...self::PARAMS,'principal'=>'account-b']],
        ] as [$account,$method,$params]) {
            try { $this->application->invoke($account,$method,$params);self::fail('Unauthorized invocation accepted'); }
            catch(RpcFailure $error){ self::assertSame(-32003,$error->rpcCode); }
        }
        self::assertSame(0,(int)$this->connection->query('SELECT COUNT(*) FROM workflow_rpc_states')->fetchColumn());
        self::assertSame(0,(int)$this->connection->query('SELECT COUNT(*) FROM workflow_queue_runs')->fetchColumn());
    }

    public function testSameSubjectAndLogicalIdAcrossAccountsRemainIndependent(): void
    {
        $first=$this->application->invoke('account-a','flow.start',self::PARAMS)['state'];
        $second=$this->application->invoke('account-b','flow.start',self::PARAMS)['state'];
        self::assertSame('same-run',$first['run']['id']);
        self::assertSame('same-run',$second['run']['id']);
        self::assertNotSame($first['metadata']['execution_id'],$second['metadata']['execution_id']);
        self::assertSame('queue',$first['metadata']['execution_driver']);
        self::assertSame(2,(int)$this->connection->query('SELECT COUNT(*) FROM workflow_queue_runs')->fetchColumn());
        self::assertSame('cancelled',$this->application->invoke('account-a','flow.cancel',self::PARAMS)['state']['status']);
        self::assertNotSame('cancelled',$this->application->invoke('account-b','flow.get',self::PARAMS)['state']['status']);
    }

    public function testRpcCannotRegisterOperationsOrSwitchExecutionAuthority(): void
    {
        try { $this->application->invoke('account-a','ows.register',self::PARAMS);self::fail('Client authoring enabled'); }
        catch(RpcFailure $error){self::assertSame(-32601,$error->rpcCode);}
        $this->expectException(RpcFailure::class);
        $this->application->invoke('account-a','flow.start',[...self::PARAMS,'driver'=>'inline']);
    }

    public function testGrantRequiresExactIdentitiesAndKnownActions(): void
    {
        $this->expectException(\InvalidArgumentException::class);
        RunGrant::fromArray(['account'=>'account-a','flow_key'=>'order','subject'=>['type'=>'order','id'=>'*'],'actions'=>['flow.start']]);
    }
}
