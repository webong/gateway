<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver\Tests\Runtime;

use PDO;
use PHPUnit\Framework\TestCase;
use Webong\Gateway\Weaver\{ApprovedOperations,OperationalSchema,RuntimeConfiguration,RuntimeFactory};

final class RuntimeFactoryTest extends TestCase
{
    private string $path;
    protected function setUp(): void {$this->path=tempnam(sys_get_temp_dir(),'gateway-workflow-');}
    protected function tearDown(): void {foreach([$this->path,$this->path.'-wal',$this->path.'-shm'] as $path){if(is_file($path)){unlink($path);}}}
    private function configuration(int $version): RuntimeConfiguration
    {
        return new RuntimeConfiguration(['schema_version'=>1,'database_ref'=>'database','authentication_ref'=>'rpc',
            'operations'=>ApprovedOperations::KEYS,'accounts'=>[['id'=>'account','definitions'=>[['key'=>'order','version'=>$version,
                'steps'=>[['id'=>'approve','metadata'=>['operation'=>'gateway.approval']],['id'=>'fulfil-v'.$version,'depends_on'=>['approve'],'metadata'=>['operation'=>'gateway.complete']]]]],
                'grants'=>[['account'=>'account','flow_key'=>'order','subject'=>['type'=>'order','id'=>'42'],'actions'=>['flow.start','flow.get','flow.complete','flow.cancel']]]]]]);
    }
    public function testRestartAndDefinitionReplacementPreservePinnedRunAndCallbacks(): void
    {
        $secret=['dsn'=>'sqlite:'.$this->path];
        OperationalSchema::install(new PDO($secret['dsn']));
        $runtime=new RuntimeFactory($this->configuration(1),$secret);
        $params=['flow_key'=>'order','run_id'=>'logical','subject'=>['type'=>'order','id'=>'42']];
        $application=$runtime->application('account');
        $application->invoke('account','flow.start',$params);
        self::assertTrue($runtime->worker()->tick(30));
        $waiting=$application->invoke('account','flow.get',$params)['state'];
        self::assertTrue($waiting['steps']['approve']['metadata']['deferred']);
        unset($application,$runtime);
        $replacement=new RuntimeFactory($this->configuration(2),$secret);
        $application=$replacement->application('account');
        $callback=[...$params,'step_id'=>'approve','attempt'=>1,'idempotency_key'=>'pre-upgrade-callback','result'=>['status'=>'completed']];
        $application->invoke('account','flow.complete',$callback);
        $application->invoke('account','flow.complete',$callback);
        self::assertTrue($replacement->worker()->tick(30));
        $state=$application->invoke('account','flow.get',$params)['state'];
        self::assertSame('completed',$state['status']);
        self::assertSame('logical',$state['run']['id']);
        self::assertSame(1,$state['run']['definition']['version']);
        self::assertSame('completed',$state['steps']['fulfil-v1']['status']);
        self::assertArrayNotHasKey('fulfil-v2',$state['steps']);
    }
    public function testActivationNeverInstallsAnEmptyDatabase(): void
    {
        $this->expectException(\RuntimeException::class);
        new RuntimeFactory($this->configuration(1),['dsn'=>'sqlite:'.$this->path]);
    }
}
