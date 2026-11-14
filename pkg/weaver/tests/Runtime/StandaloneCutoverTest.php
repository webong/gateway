<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver\Tests\Runtime;

use PDO;
use PHPUnit\Framework\TestCase;
use PHPUnit\Framework\Attributes\DataProvider;
use Webong\Gateway\Weaver\{ApprovedOperations, PublishedDefinitions, RuntimeConfiguration, RuntimeFactory, StandaloneCutover};
use Webong\WorkFlow\Services\{DefaultFlowStateSerializer, FlowExecutionDispatcher};

final class StandaloneCutoverTest extends TestCase
{
    private array $files=[];
    private array $databases=[];
    private string $backend='sqlite';

    public static function backends(): array {return [['sqlite'],['postgres'],['mysql']];}

    protected function tearDown(): void
    {
        foreach($this->databases as [$admin,$name]){
            $quote=$this->backend==='mysql'?'`':'"';
            $admin->exec('DROP DATABASE '.$quote.$name.$quote.($this->backend==='postgres'?' WITH (FORCE)':''));
        }
        foreach($this->files as $file){foreach([$file,$file.'-wal',$file.'-shm'] as $path){if(is_file($path)){unlink($path);}}}
    }

    private function database(): array
    {
        if($this->backend!=='sqlite'){
            $key=$this->backend==='postgres'?'POSTGRES':'MYSQL';
            $dsn=getenv('WORK_FLOW_'.$key.'_DSN');
            $user=$key==='MYSQL'?'root':getenv('WORK_FLOW_'.$key.'_USER');
            $password=$key==='MYSQL'?'disposable-workflow-root':getenv('WORK_FLOW_'.$key.'_PASSWORD');
            self::assertIsString($dsn);self::assertStringContainsString('dbname=workflow_test',$dsn);
            $admin=new PDO($dsn,$user,$password,[PDO::ATTR_ERRMODE=>PDO::ERRMODE_EXCEPTION]);
            $name='workflow_cutover_'.bin2hex(random_bytes(6));$quote=$key==='MYSQL'?'`':'"';
            $admin->exec('CREATE DATABASE '.$quote.$name.$quote.($key==='MYSQL'?' CHARACTER SET utf8mb4 COLLATE utf8mb4_bin':''));
            $this->databases[]=[$admin,$name];
            $copyDsn=str_replace('dbname=workflow_test','dbname='.$name,$dsn);
            return [new PDO($copyDsn,$user,$password,[PDO::ATTR_ERRMODE=>PDO::ERRMODE_EXCEPTION]),['dsn'=>$copyDsn,'username'=>$user,'password'=>$password]];
        }
        $file=tempnam(sys_get_temp_dir(),'weaver-cutover-');$this->files[]=$file;
        return [new PDO('sqlite:'.$file,options:[PDO::ATTR_ERRMODE=>PDO::ERRMODE_EXCEPTION]),['dsn'=>'sqlite:'.$file]];
    }

    private function definition(int $version=1): array
    {
        return ['key'=>'order','version'=>$version,'steps'=>[
            ['id'=>'approve','metadata'=>['operation'=>'gateway.approval']],
            ['id'=>'fulfil-v'.$version,'depends_on'=>['approve'],'metadata'=>['operation'=>'gateway.complete']],
        ]];
    }

    private function configuration(): RuntimeConfiguration
    {
        return new RuntimeConfiguration(['schema_version'=>1,'database_ref'=>'database','authentication_ref'=>'authentication','operations'=>ApprovedOperations::KEYS,
            'accounts'=>[['id'=>'account-a','definitions'=>[$this->definition(2)],'grants'=>[['account'=>'account-a','flow_key'=>'order','subject'=>['type'=>'order','id'=>'42'],'actions'=>['flow.get','flow.start','flow.complete','flow.cancel','flow.resume']]]]]]);
    }

    private function legacy(PDO $pdo,bool $install=true): array
    {
        // Load the exact original application classes shipped by the pinned source
        // image. The engine itself still comes from the released core consumer.
        spl_autoload_register(static function(string $class):void{
            $prefix='Webong\\WorkFlow\\App\\';
            if(str_starts_with($class,$prefix)){
                $path='/app/app/src/'.str_replace('\\','/',substr($class,strlen($prefix))).'.php';
                if(is_file($path)){require_once $path;}
            }
        });
        if($install){
            $pdo->exec(file_get_contents('/extension/schema/'.$this->backend.'.sql'));
            \Webong\WorkFlow\App\QueueScheduler::install($pdo,3);
        }
        $queue=new \Webong\WorkFlow\App\QueueScheduler($pdo);
        $operations=new ApprovedOperations(ApprovedOperations::KEYS);
        $application=new \Webong\WorkFlow\App\FlowRpcApplication(new PublishedDefinitions([$this->definition()],$operations),
            match($this->backend){'sqlite'=>new \Webong\WorkFlow\App\PdoSqliteFlowStateStoreFactory($pdo,new DefaultFlowStateSerializer()),'postgres'=>new \Webong\WorkFlow\App\PdoPostgresFlowStateStoreFactory($pdo,new DefaultFlowStateSerializer()),'mysql'=>new \Webong\WorkFlow\App\PdoMySqlFlowStateStoreFactory($pdo,new DefaultFlowStateSerializer())},new FlowExecutionDispatcher([]),
            queue:new \Webong\WorkFlow\App\QueueRpcRuntime($queue));
        return [$application,new \Webong\WorkFlow\App\QueueWorker($queue,[$operations]),$queue];
    }

    #[DataProvider('backends')]
    public function testCopiedRunAcceptsOldCallbackAndRollsBackWithCurrentProgress(string $backend): void
    {
        $this->backend=$backend;
        [$source]=$this->database();[$old,$worker]=$this->legacy($source);
        $params=['flow_key'=>'order','run_id'=>'pre-cutover','subject'=>['type'=>'order','id'=>'42'],'driver'=>'queue','context'=>['original'=>true]];
        $old->invoke('flow.start',$params);self::assertTrue($worker->tick(30));
        $waiting=$old->invoke('flow.get',$params)['state'];
        $callback=[...$params,'step_id'=>'approve','attempt'=>1,'idempotency_key'=>'issued-before-cutover','result'=>['status'=>'completed']];
        [$copy,$secret]=$this->database();
        $snapshot=$source->query('SELECT * FROM workflow_queue_runs')->fetchAll(PDO::FETCH_ASSOC);
        $claims=$source->query('SELECT * FROM workflow_queue_claims')->fetchAll(PDO::FETCH_ASSOC);
        $outbox=$source->query('SELECT * FROM workflow_queue_outbox ORDER BY id')->fetchAll(PDO::FETCH_ASSOC);
        StandaloneCutover::copy($source,$copy,$this->configuration(),[['account'=>'account-a','subject'=>['type'=>'order','id'=>'42']]]);
        self::assertSame($snapshot,$source->query('SELECT * FROM workflow_queue_runs')->fetchAll(PDO::FETCH_ASSOC),'Source changed during copy');
        self::assertSame($snapshot,$copy->query('SELECT * FROM workflow_queue_runs')->fetchAll(PDO::FETCH_ASSOC),'Queue identity or pinned state changed');
        self::assertSame($claims,$copy->query('SELECT * FROM workflow_queue_claims')->fetchAll(PDO::FETCH_ASSOC));
        self::assertSame($outbox,$copy->query('SELECT * FROM workflow_queue_outbox ORDER BY id')->fetchAll(PDO::FETCH_ASSOC));
        self::assertSame(3,(int)$copy->query('SELECT capacity FROM workflow_queue_lock WHERE id=1')->fetchColumn());
        $runtime=new RuntimeFactory($this->configuration(),$secret);$app=$runtime->application('account-a');
        $state=$app->invoke('account-a','flow.get',$params)['state'];
        self::assertSame($waiting['metadata']['execution_id'],$state['metadata']['execution_id']);
        self::assertSame(1,$state['run']['definition']['version']);
        $app->invoke('account-a','flow.complete',$callback);
        $app->invoke('account-a','flow.complete',$callback);
        self::assertTrue($runtime->worker()->tick(30));
        $completed=$app->invoke('account-a','flow.get',$params)['state'];
        self::assertSame('completed',$completed['status']);
        self::assertSame('completed',$completed['steps']['fulfil-v1']['status']);
        self::assertArrayNotHasKey('fulfil-v2',$completed['steps']);
        self::assertArrayNotHasKey('_rpc_import',$completed['metadata']);
        [$rollback]=$this->database();StandaloneCutover::rollback($copy,$rollback);
        [$restored]=$this->legacy($rollback,false);
        $count=(int)$rollback->query('SELECT COUNT(*) FROM workflow_queue_outbox')->fetchColumn();
        $restored->invoke('flow.complete',$callback);
        self::assertSame($count,(int)$rollback->query('SELECT COUNT(*) FROM workflow_queue_outbox')->fetchColumn(),'Callback replay created new events after rollback');
        self::assertSame($completed,$restored->invoke('flow.get',$params)['state']);
        self::assertSame($waiting,$old->invoke('flow.get',$params)['state'],'Stopped original source must remain unchanged');
    }
    public function testUndrainedClaimsRefuseCopyBeforeDestinationWrites(): void
    {
        [$source]=$this->database();[$old,,$queue]=$this->legacy($source);
        $params=['flow_key'=>'order','run_id'=>'active','subject'=>['type'=>'order','id'=>'42'],'driver'=>'queue'];
        $old->invoke('flow.start',$params);$queue->claim(1,30);[$copy]=$this->database();
        $before=$source->query('SELECT * FROM workflow_queue_claims')->fetchAll(PDO::FETCH_ASSOC);
        try{StandaloneCutover::copy($source,$copy,$this->configuration(),[['account'=>'account-a','subject'=>['type'=>'order','id'=>'42']]]);self::fail('Active claim copied');}
        catch(\RuntimeException $error){self::assertStringContainsString('Drain or reconcile',$error->getMessage());}
        self::assertSame($before,$source->query('SELECT * FROM workflow_queue_claims')->fetchAll(PDO::FETCH_ASSOC));
        self::assertSame(0,(int)$copy->query("SELECT COUNT(*) FROM sqlite_master WHERE type='table'")->fetchColumn());
    }

    public function testNewNativeRunRefusesLegacyRollback(): void
    {
        [$source]=$this->database();[$old,$worker]=$this->legacy($source);
        $params=['flow_key'=>'order','run_id'=>'old','subject'=>['type'=>'order','id'=>'42'],'driver'=>'queue'];
        $old->invoke('flow.start',$params);$worker->tick(30);[$copy,$secret]=$this->database();
        StandaloneCutover::copy($source,$copy,$this->configuration(),[['account'=>'account-a','subject'=>['type'=>'order','id'=>'42']]]);
        $runtime=new RuntimeFactory($this->configuration(),$secret);
        $runtime->application('account-a')->invoke('account-a','flow.start',[...$params,'run_id'=>'new']);
        [$rollback]=$this->database();$this->expectException(\RuntimeException::class);
        StandaloneCutover::rollback($copy,$rollback);
    }

    public function testImportedMissingQueueNeverReinitializesAndTamperedScopeIsDenied(): void
    {
        [$source]=$this->database();[$old,$worker]=$this->legacy($source);
        $params=['flow_key'=>'order','run_id'=>'old','subject'=>['type'=>'order','id'=>'42'],'driver'=>'queue'];
        $old->invoke('flow.start',$params);$worker->tick(30);[$copy,$secret]=$this->database();
        StandaloneCutover::copy($source,$copy,$this->configuration(),[['account'=>'account-a','subject'=>['type'=>'order','id'=>'42']]]);
        $runtime=new RuntimeFactory($this->configuration(),$secret);$app=$runtime->application('account-a');
        $row=$copy->query('SELECT state FROM workflow_rpc_states')->fetchColumn();$document=json_decode($row,true);
        $document['metadata']['_rpc_import']['account']='account-b';
        $copy->prepare('UPDATE workflow_rpc_states SET state=?')->execute([json_encode($document)]);
        try{$app->invoke('account-a','flow.get',$params);self::fail('Imported scope tampering accepted');}
        catch(\Webong\Gateway\Weaver\RpcFailure $error){self::assertSame(-32009,$error->rpcCode);}
        $copy->prepare('UPDATE workflow_rpc_states SET state=?')->execute([$row]);
        $copy->exec('DELETE FROM workflow_queue_runs');
        try{$app->invoke('account-a','flow.start',$params);self::fail('Missing imported execution recreated');}
        catch(\Webong\Gateway\Weaver\RpcFailure $error){self::assertSame(-32009,$error->rpcCode);}
        self::assertSame(0,(int)$copy->query('SELECT COUNT(*) FROM workflow_queue_runs')->fetchColumn());
    }

    public function testOfflineSourceIsReadOnlyAndDoesNotChangeSqliteJournalOrCreateMissingFiles(): void
    {
        [$source,$secret]=$this->database();$source->exec('CREATE TABLE sentinel (value INTEGER)');
        $source->exec('INSERT INTO sentinel VALUES (42)');$source->exec('PRAGMA journal_mode=DELETE');
        $file=substr($secret['dsn'],7);$before=hash_file('sha256',$file);chmod($file,0444);
        $read=\Webong\Gateway\Weaver\ConnectionFactory::readOnlySource($secret);
        self::assertSame('delete',$read->query('PRAGMA journal_mode')->fetchColumn());
        self::assertSame(42,(int)$read->query('SELECT value FROM sentinel')->fetchColumn());
        try{$read->exec('DELETE FROM sentinel');self::fail('Source accepted writes');}catch(\PDOException){}
        unset($read);self::assertSame($before,hash_file('sha256',$file));chmod($file,0600);
        $missing=$file.'-missing';
        try{\Webong\Gateway\Weaver\ConnectionFactory::readOnlySource(['dsn'=>'sqlite:'.$missing]);self::fail('Missing source opened');}
        catch(\RuntimeException){self::assertFileDoesNotExist($missing);}
    }

    public function testNonBinaryMysqlSourceRequiresIdentityAudit(): void
    {
        $this->backend='mysql';[$source]=$this->database();[$old,$worker]=$this->legacy($source);
        $params=['flow_key'=>'order','run_id'=>'legacy','subject'=>['type'=>'order','id'=>'42'],'driver'=>'queue'];
        $old->invoke('flow.start',$params);$worker->tick(30);
        $source->exec('ALTER TABLE workflow_rpc_states CONVERT TO CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci');
        [$copy]=$this->database();$this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage('Audit and upgrade legacy MySQL identities');
        StandaloneCutover::copy($source,$copy,$this->configuration(),[['account'=>'account-a','subject'=>['type'=>'order','id'=>'42']]]);
    }

    public function testReservedOutboxBlocksCutoverWithoutChangingTheSource(): void
    {
        [$source]=$this->database();[$old,$worker,$queue]=$this->legacy($source);
        $params=['flow_key'=>'order','run_id'=>'legacy','subject'=>['type'=>'order','id'=>'42'],'driver'=>'queue'];
        $old->invoke('flow.start',$params);$worker->tick(30);$queue->reserveEvents(1,30);
        $events=$source->query('SELECT * FROM workflow_queue_outbox ORDER BY id')->fetchAll(PDO::FETCH_ASSOC);
        [$copy]=$this->database();
        try{StandaloneCutover::copy($source,$copy,$this->configuration(),[['account'=>'account-a','subject'=>['type'=>'order','id'=>'42']]]);self::fail('Reserved outbox copied');}
        catch(\RuntimeException $error){self::assertStringContainsString('outbox reservations',$error->getMessage());}
        self::assertSame($events,$source->query('SELECT * FROM workflow_queue_outbox ORDER BY id')->fetchAll(PDO::FETCH_ASSOC));
        self::assertSame(0,(int)$copy->query("SELECT COUNT(*) FROM sqlite_master WHERE type='table'")->fetchColumn());
    }

}
