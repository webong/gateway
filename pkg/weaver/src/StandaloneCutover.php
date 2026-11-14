<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver;

use PDO;
use RuntimeException;
use Throwable;
use Webong\WorkFlow\Services\CanonicalFlowPayload;
use Webong\WorkFlow\ValueObjects\{FlowState,FlowStateSubject};

/** Offline copy only: callers stop writers and switch endpoints after verification. */
final class StandaloneCutover
{
    private const TABLES=['workflow_rpc_states','workflow_queue_lock','workflow_queue_runs','workflow_queue_claims','workflow_queue_outbox'];

    /** @param list<array{account:string,subject:array{type:string,id:string}}> $assignments */
    public static function copy(PDO $source, PDO $destination, RuntimeConfiguration $configuration, array $assignments): void
    {
        if(OperationalSchema::driver($source)!==OperationalSchema::driver($destination)){throw new RuntimeException('Cross-engine cutover is unsupported; preserve the source SQL driver.');}
        if($source===$destination){throw new RuntimeException('Source and destination must be separate databases.');}
        $snapshot=self::snapshot($source);
        $mappings=[];
        foreach($assignments as $assignment){
            if(!is_array($assignment) || array_diff(array_keys($assignment),['account','subject'])!==[]
                || !is_string($assignment['account']??null) || !isset($configuration->definitions[$assignment['account']])
                || !is_array($assignment['subject']??null) || count($assignment['subject'])!==2 || array_diff(array_keys($assignment['subject']),['type','id'])!==[]
                || !is_string($assignment['subject']['type']) || !is_string($assignment['subject']['id'])) {
                throw new RuntimeException('Each source subject needs one explicit configured account assignment.');
            }
            $key=json_encode([$assignment['subject']['type'],$assignment['subject']['id']],JSON_THROW_ON_ERROR);
            if(isset($mappings[$key])){throw new RuntimeException('Ambiguous source subject assignment.');}
            $mappings[$key]=$assignment['account'];
        }
        foreach($snapshot['workflow_rpc_states'] as &$row){
            $subject=['type'=>$row['subject_type'],'id'=>$row['subject_id']];
            $account=$mappings[json_encode([$subject['type'],$subject['id']],JSON_THROW_ON_ERROR)]??throw new RuntimeException('Unassigned standalone subject.');
            $state=FlowState::fromArray(self::decode($row['state']));
            if(isset($state->metadata['_rpc_import'])){throw new RuntimeException('Source is already imported.');}
            $run=$state->run??throw new RuntimeException('Missing pinned run.');
            // Pinned versions may precede the selected current version, but all
            // executable operations must still belong to the deployed catalog.
            new PublishedDefinitions([$run->definition->toArray()],$configuration->operations);
            $allowed=false;
            foreach($configuration->grants as $grant){
                if($grant->account===$account && $grant->flowKey===$run->definition->key && $grant->subjectType===$subject['type'] && $grant->subjectId===$subject['id']){$allowed=true;break;}
            }
            if(!$allowed){throw new RuntimeException('Imported run has no explicit account/flow/subject grant.');}
            $scoped=RpcRunIdentity::accountSubject($account,new FlowStateSubject($subject['type'],$subject['id']));
            $document=self::decode($row['state']);
            $document['metadata']['_rpc_import']=['version'=>1,'account'=>$account,'subject'=>$subject];
            $row['subject_type']=$scoped->type;$row['subject_id']=$scoped->id;
            $row['state']=json_encode($document,JSON_THROW_ON_ERROR);
        }
        unset($row);
        self::write($destination,$snapshot,true);
    }

    /** Roll current progress into a fresh legacy-compatible DB; never restore stale effects. */
    public static function rollback(PDO $source, PDO $destination): void
    {
        if(OperationalSchema::driver($source)!==OperationalSchema::driver($destination)){throw new RuntimeException('Cross-engine cutover is unsupported; preserve the source SQL driver.');}
        if($source===$destination){throw new RuntimeException('Source and destination must be separate databases.');}
        $snapshot=self::snapshot($source,true);
        foreach($snapshot['workflow_rpc_states'] as &$row){
            $state=FlowState::fromArray(self::decode($row['state']));
            $subject=new FlowStateSubject($row['subject_type'],$row['subject_id']);
            if(ImportedRunIdentity::resolve($subject,$state)===null){throw new RuntimeException('New Weaver-native runs require reconciliation before legacy rollback.');}
            $document=self::decode($row['state']);$marker=$document['metadata']['_rpc_import'];
            $row['subject_type']=$marker['subject']['type'];$row['subject_id']=$marker['subject']['id'];
            unset($document['metadata']['_rpc_import']);
            $row['state']=json_encode($document,JSON_THROW_ON_ERROR);
        }
        unset($row);
        self::write($destination,$snapshot,false);
    }

    private static function snapshot(PDO $source, bool $imported=false): array
    {
        $driver=OperationalSchema::driver($source);
        if($source->inTransaction()){throw new RuntimeException('Cutover requires an idle offline connection.');}
        if($driver==='mysql'){$source->exec('SET TRANSACTION ISOLATION LEVEL REPEATABLE READ');$source->exec('SET TRANSACTION READ ONLY');}
        $source->beginTransaction();
        try {
            if($driver==='postgres'){$source->exec('SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY');}
            if($driver==='mysql'){
                $columns=$source->query("SELECT COLLATION_NAME FROM information_schema.columns WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='workflow_rpc_states' AND COLUMN_NAME IN ('subject_type','subject_id','flow_key')")->fetchAll(PDO::FETCH_COLUMN);
                if(count($columns)!==3 || array_filter($columns,static fn($collation)=>$collation!=='utf8mb4_bin')!==[]){throw new RuntimeException('Audit and upgrade legacy MySQL identities to utf8mb4_bin before cutover.');}
            }
            $snapshot=[];
            foreach(self::TABLES as $table){$snapshot[$table]=$source->query('SELECT * FROM '.$table)->fetchAll(PDO::FETCH_ASSOC);}
            foreach($snapshot['workflow_queue_claims'] as $claim){
                if(in_array($claim['status'],['claimed','executing'],true)){throw new RuntimeException('Drain or reconcile all claims before copying.');}
            }
            foreach($snapshot['workflow_queue_outbox'] as $event){
                if($event['status']==='delivering'){throw new RuntimeException('Drain or reconcile outbox reservations before copying.');}
            }
            if(count($snapshot['workflow_queue_lock'])!==1 || (int)$snapshot['workflow_queue_lock'][0]['id']!==1){throw new RuntimeException('Invalid queue capacity record.');}
            $runs=[];
            foreach($snapshot['workflow_queue_runs'] as $row){$runs[$row['id']]=$row;}
            $seen=[];
            foreach($snapshot['workflow_rpc_states'] as $row){
                $state=FlowState::fromArray(self::decode($row['state']));
                $run=$state->run??throw new RuntimeException('Uninitialized routing reservation.');
                $subject=new FlowStateSubject($row['subject_type'],$row['subject_id']);
                $key=$imported?ImportedRunIdentity::resolve($subject,$state):RpcRunIdentity::executionId($subject,$run->definition->key,$run->id);
                if((int)$row['initialized']!==1 || ($state->metadata['_rpc_authority']??null)!=='queue-v1' || $key===null || ($state->metadata['execution_id']??null)!==$key || !isset($runs[$key]) || isset($seen[$key])){
                    throw new RuntimeException('Copy requires unique, initialized queue-v1 routing and existing queue records.');
                }
                $queued=FlowState::fromArray(self::decode($runs[$key]['state']));
                $run->assertDefinition($queued->run?->definition??throw new RuntimeException('Queue run is missing'));
                if($queued->run->id!==$run->id || CanonicalFlowPayload::fingerprint(self::decode($runs[$key]['context']))!==CanonicalFlowPayload::fingerprint($state->metadata['_rpc_context']??[])){
                    throw new RuntimeException('Queue identity or pinned context differs from its routing reservation.');
                }
                $seen[$key]=true;
            }
            if(count($seen)!==count($runs)){throw new RuntimeException('Unrouted queue records require explicit reconciliation.');}
            $source->rollBack();return $snapshot;
        }catch(Throwable $error){if($source->inTransaction()){$source->rollBack();}throw $error;}
    }

    private static function write(PDO $destination,array $snapshot,bool $weaver): void
    {
        // Existing data is never overwritten, including interrupted MySQL DDL.
        foreach([...self::TABLES,'gateway_workflow_schema'] as $table){
            try{$destination->query('SELECT 1 FROM '.$table.' WHERE 1=0');}
            catch(\PDOException){continue;}
            throw new RuntimeException('Cutover destination must have no existing operational tables.');
        }
        $capacity=(int)$snapshot['workflow_queue_lock'][0]['capacity'];
        if($weaver){OperationalSchema::install($destination,$capacity);}
        else {
            $driver=OperationalSchema::driver($destination);
            $destination->exec(file_get_contents(__DIR__.'/../schema/'.$driver.'.sql'));
            QueueScheduler::install($destination,$capacity);
        }
        $destination->beginTransaction();
        try {
            foreach(self::TABLES as $table){
                if($table==='workflow_queue_lock'){continue;}
                // Also rejects a versioned but non-empty Weaver destination.
                if((int)$destination->query('SELECT COUNT(*) FROM '.$table)->fetchColumn()!==0){throw new RuntimeException('Cutover destination must be empty.');}
                foreach($snapshot[$table] as $row){
                    $columns=array_keys($row);
                    $statement=$destination->prepare('INSERT INTO '.$table.' ('.implode(',',$columns).') VALUES ('.implode(',',array_fill(0,count($columns),'?')).')');
                    $statement->execute(array_values($row));
                }
            }
            $destination->commit();
        }catch(Throwable $error){if($destination->inTransaction()){$destination->rollBack();}throw $error;}
    }

    private static function decode(string $value): array
    {
        $document=json_decode($value,true,128,JSON_THROW_ON_ERROR);
        if(!is_array($document)){throw new RuntimeException('Invalid stored operational document.');}
        return $document;
    }
}
