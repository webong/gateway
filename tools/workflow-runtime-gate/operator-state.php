<?php
// Offline fixture assertions. Only disposable data; source contexts stay mode 0600.
$runtime=require '/gate/operator-legacy-runtime.php';$pdo=$runtime['connection'];$mode=$argv[1];
$tables=['workflow_rpc_states','workflow_queue_lock','workflow_queue_runs','workflow_queue_claims','workflow_queue_outbox'];
function rows(PDO $pdo,string $table):array{
 $rows=$pdo->query('SELECT * FROM '.$table)->fetchAll(PDO::FETCH_ASSOC);
 usort($rows,fn($a,$b)=>strcmp(json_encode($a),json_encode($b)));return $rows;
}
function check(bool $value,string $message):void{if(!$value)throw new RuntimeException($message);}
if($mode==='prepare'){
 $pdo->exec(file_get_contents('/extension/schema/sqlite.sql'));
 Webong\WorkFlow\App\QueueScheduler::install($pdo,3);
 chown('/state/legacy.sqlite',10001);chgrp('/state/legacy.sqlite',10001);chmod('/state/legacy.sqlite',0600);
}elseif($mode==='backup'){
 check($pdo->query('PRAGMA journal_mode')->fetchColumn()==='delete','Fixture requires a closed DELETE-journal source');
 $snapshot=[];foreach($tables as $table)$snapshot[$table]=rows($pdo,$table);
 $sha=hash_file('sha256','/state/legacy.sqlite');
 foreach(['backup','backup-restored','reference'] as $name){
  check(copy('/state/legacy.sqlite','/state/'.$name.'.sqlite'),'Backup copy failed');chmod('/state/'.$name.'.sqlite',0600);
  chown('/state/'.$name.'.sqlite',10001);chgrp('/state/'.$name.'.sqlite',10001);
  check(hash_file('sha256','/state/'.$name.'.sqlite')===$sha,'Backup bytes differ');
  $read=new PDO('sqlite:/state/'.$name.'.sqlite');check($read->query('PRAGMA integrity_check')->fetchColumn()==='ok','Backup integrity failed');
 }
 file_put_contents('/state/operator-snapshot.json',json_encode(['sha256'=>$sha,'restored_backup_sha256_before_probe'=>hash_file('sha256','/state/backup-restored.sqlite'),'tables'=>$snapshot],JSON_THROW_ON_ERROR));chmod('/state/operator-snapshot.json',0644);
 $config=require '/gate/operator-configuration.php';
 $context=['source'=>['dsn'=>'sqlite:/state/legacy.sqlite'],'destination'=>['dsn'=>'sqlite:/state/workflow.sqlite'],
  'configuration'=>$config,'assignments'=>[['account'=>'gate-account','subject'=>['type'=>'order','id'=>'operator-subject']]]];
 foreach(['copy'=>$context,'rollback'=>['source'=>$context['destination'],'destination'=>['dsn'=>'sqlite:/state/rollback.sqlite']]] as $name=>$document){
  file_put_contents('/state/operator-'.$name.'.json',json_encode($document,JSON_THROW_ON_ERROR));chmod('/state/operator-'.$name.'.json',0600);
 }
}elseif($mode==='reference-key'){
 $app=require '/app/app/bootstrap.example.php';$baseline=json_decode(file_get_contents('/state/operator-baseline.json'),true);
 $app->invoke('flow.complete',$baseline['callback']);$claim=$runtime['queue']->claim(1,30)[0]??throw new RuntimeException('Reference claim missing');
 check($claim->step->id==='deliver','Reference selected the wrong operation');
 file_put_contents('/state/operator-effect-key', $claim->metadata['idempotency_key']);chmod('/state/operator-effect-key',0644);
}elseif($mode==='verify-copy'){
 $snapshot=json_decode(file_get_contents('/state/operator-snapshot.json'),true);
 check(hash_file('sha256','/state/legacy.sqlite')===$snapshot['sha256'],'Stopped legacy source changed');
 $copy=new PDO('sqlite:/state/workflow.sqlite');
 foreach(array_slice($tables,1) as $table)check(rows($copy,$table)===$snapshot['tables'][$table],'Copied queue/claim/outbox/capacity changed');
 check($copy->query('PRAGMA integrity_check')->fetchColumn()==='ok','Destination integrity failed');
 // Explicit operator ACL provisioning for the actual host's runtime identity.
 chown('/state/workflow.sqlite',(int)getenv('OPERATOR_RUNTIME_UID'));chgrp('/state/workflow.sqlite',(int)getenv('OPERATOR_RUNTIME_GID'));chmod('/state/workflow.sqlite',0660);
}elseif($mode==='drained'){
 $read=new PDO('sqlite:/state/workflow.sqlite');
 $active=(int)$read->query("SELECT COUNT(*) FROM workflow_queue_claims WHERE status IN ('claimed','executing')")->fetchColumn();
 $pending=(int)$read->query("SELECT COUNT(*) FROM workflow_queue_outbox WHERE status='delivering' OR (type='work.ready' AND status!='delivered')")->fetchColumn();
 check($active===0 && $pending===0,'Weaver is not drained');
}elseif(in_array($mode,['verify-rollback','verify-replay'],true)){
 $snapshot=json_decode(file_get_contents('/state/operator-snapshot.json'),true);
 check(hash_file('sha256','/state/legacy.sqlite')===$snapshot['sha256'],'Original source changed after acceptance');
 $current=new PDO('sqlite:/state/workflow.sqlite');
 foreach(array_slice($tables,$mode==='verify-rollback'?1:2) as $table)check(rows($pdo,$table)===rows($current,$table),'Rollback lost current queue progress');
 $capacity=(int)$current->query('SELECT capacity FROM workflow_queue_lock WHERE id=1')->fetchColumn();
 check((int)$pdo->query('SELECT capacity FROM workflow_queue_lock WHERE id=1')->fetchColumn()===$capacity,'Rollback capacity changed');
 check((int)$pdo->query('SELECT revision FROM workflow_queue_lock WHERE id=1')->fetchColumn()>=(int)$current->query('SELECT revision FROM workflow_queue_lock WHERE id=1')->fetchColumn(),'Legacy coordination revision regressed');
 check($pdo->query('PRAGMA integrity_check')->fetchColumn()==='ok','Rollback integrity failed');
 chown('/state/rollback.sqlite',10001);chgrp('/state/rollback.sqlite',10001);chmod('/state/rollback.sqlite',0600);
 $app=require '/app/app/bootstrap.example.php';$baseline=json_decode(file_get_contents('/state/operator-baseline.json'),true);
 $complete=json_decode(file_get_contents('/state/operator-completed.json'),true);
 check($app->invoke('flow.get',$baseline['params'])['state']===$complete,'Legacy cannot read current progress');
 $app->invoke('flow.complete',$baseline['callback']);
 check(!(new Webong\WorkFlow\App\QueueWorker($runtime['queue'],$runtime['executors']))->tick(30),'Completed effect replayed after rollback');
 $provider=new PDO('sqlite:/state/provider-effects.sqlite');$effects=$provider->query('SELECT receipt,requests FROM effects')->fetchAll(PDO::FETCH_ASSOC);
 check(count($effects)===1 && $effects[0]['receipt']===file_get_contents('/state/operator-effect-key') && (int)$effects[0]['requests']===2,'Provider effect or original key changed');
}elseif($mode==='report'){
 $snapshot=json_decode(file_get_contents('/state/operator-snapshot.json'),true);
 $baseline=json_decode(file_get_contents('/state/operator-baseline.json'),true);
 $provider=new PDO('sqlite:/state/provider-effects.sqlite');
 $effects=$provider->query('SELECT receipt,scope,requests FROM effects')->fetchAll(PDO::FETCH_ASSOC);
 echo json_encode(['source_sha256'=>$snapshot['sha256'],'backup_sha256'=>hash_file('sha256','/state/backup.sqlite'),
  'restored_backup_sha256_before_probe'=>$snapshot['restored_backup_sha256_before_probe'],'backup_restore_verified'=>true,
  'source_execution_id'=>$baseline['waiting']['metadata']['execution_id'],
  'queue_capacity'=>$snapshot['tables']['workflow_queue_lock'][0]['capacity'],
  'copied_coordination_revision'=>$snapshot['tables']['workflow_queue_lock'][0]['revision'],
  'provider_effects'=>$effects],JSON_THROW_ON_ERROR);return;
}else{throw new InvalidArgumentException('Unknown operator fixture action');}
echo "PASS operator fixture $mode\n";
