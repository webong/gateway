<?php
$base=$argv[1];$phase=$argv[2]??'start';$id=['flow_key'=>'gate_order','run_id'=>'gate-logical-run','subject'=>['type'=>'order','id'=>'gate-subject']];
function rpc(string $method,array $params,string $token='disposable-runtime-gate-token'):array{
 global $base;
 $hostToken=getenv('WORKFLOW_GATE_HOST_TOKEN');
 $auth=is_string($hostToken)&&$hostToken!==''?"Authorization: Bearer $hostToken\r\nX-Gateway-Extension-Authorization: Bearer $token\r\nX-Gateway-Internal: $hostToken\r\n":"Authorization: Bearer $token\r\n";
 $context=stream_context_create(['http'=>['method'=>'POST','timeout'=>5,'ignore_errors'=>true,'header'=>"Content-Type: application/json\r\n".$auth,'content'=>json_encode(['jsonrpc'=>'2.0','id'=>37,'method'=>$method,'params'=>$params])]]);
 $body=file_get_contents($base.'/workflow/rpc',false,$context);
 $code=(int)explode(' ',$http_response_header[0])[1];
 if($token==='wrong'){if($code!==401)throw new RuntimeException('Authentication failed open');return [];}
 $value=json_decode($body,true,512,JSON_THROW_ON_ERROR);
 if($code!==200||($value['id']??null)!==37||isset($value['error']))throw new RuntimeException('RPC failed: '.json_encode($value));
 return $value['result']['state'];
}
function waitState(array $id,callable $predicate):array{
 $until=microtime(true)+20;
 do{$state=rpc('flow.get',$id);if($predicate($state))return $state;usleep(100000);}while(microtime(true)<$until);
 throw new RuntimeException('Queue did not reach expected state');
}
if(in_array($phase,['abandon','abandon-nonidempotent'],true)){
 touch('/state/pause-worker');
 $until=microtime(true)+5;while(!is_file('/state/worker-paused')&&microtime(true)<$until){usleep(10000);clearstatcache();}
 if(!is_file('/state/worker-paused'))throw new RuntimeException('Cannot pause test worker between transactions');
 $recovery=[...$id,'run_id'=>'lease-recovery','flow_key'=>$phase==='abandon'?'gate_order':'gate_no_replay'];
 rpc('flow.start',[...$recovery,'driver'=>'queue']);
 $runtime=require __DIR__.'/runtime.php';
 $claims=$runtime['queue']->claim(1,1);
 if(count($claims)!==1)throw new RuntimeException('Expected one abandoned claim');
 file_put_contents('/state/abandoned-claim', $claims[0]->claimId);
 echo "PASS: reserved an unexecuted claim before process crash\n";exit;
}
if($phase==='nonidempotent-fenced'){
 $recovery=[...$id,'run_id'=>'lease-recovery','flow_key'=>'gate_no_replay'];
 $state=waitState($recovery,fn($s)=>($s['steps']['approve']['metadata']['error_code']??null)==='claim_lost');
 if($state['steps']['approve']['attempts']!==1||($state['steps']['fulfil']['attempts']??0)!==0)throw new RuntimeException('Uncertain non-idempotent effect replayed');
 echo "PASS: uncertain non-idempotent attempt requires reconciliation; no automatic replay\n";exit;
}
if($phase==='lease-recovered'){
 $recovery=[...$id,'run_id'=>'lease-recovery'];
 $state=waitState($recovery,fn($s)=>($s['steps']['approve']['metadata']['deferred']??false)===true);
 $runtime=require __DIR__.'/runtime.php';
 if($runtime['queue']->beginExecution(file_get_contents('/state/abandoned-claim'))!==null)throw new RuntimeException('Stale claim still executable');
 rpc('flow.complete',[...$recovery,'step_id'=>'approve','attempt'=>$state['steps']['approve']['attempts'],'idempotency_key'=>'recovery-callback','result'=>['status'=>'completed']]);
 waitState($recovery,fn($s)=>$s['status']==='completed');
 echo "PASS: expired claim recovered after crash; stale claim fenced; dependent completion preserved\n";exit;
}
if($phase==='restart'){
 $state=rpc('flow.get',$id);
 if($state['status']!=='completed')throw new RuntimeException('Completed queue state lost on restart');
 echo "PASS: pinned queue state survived runtime replacement\n";exit;
}
rpc('flow.start',$id,'wrong');
rpc('flow.start',[...$id,'driver'=>'queue']);
$waiting=waitState($id,fn($s)=>($s['steps']['approve']['metadata']['deferred']??false)===true);
if(($waiting['metadata']['execution_driver']??null)!=='queue'||$waiting['run']['id']!==$id['run_id'])throw new RuntimeException('Queue authority or logical ID lost');
$other=[...$id,'subject'=>['type'=>'order','id'=>'other-subject']];
rpc('flow.start',[...$other,'driver'=>'queue']);
$otherWaiting=waitState($other,fn($s)=>($s['steps']['approve']['metadata']['deferred']??false)===true);
if($waiting['metadata']['execution_id']===$otherWaiting['metadata']['execution_id'])throw new RuntimeException('Scoped identity collided');
$completion=[...$id,'step_id'=>'approve','attempt'=>$waiting['steps']['approve']['attempts'],'idempotency_key'=>'gate-callback','result'=>['status'=>'completed','output'=>['approved'=>true]]];
rpc('flow.complete',$completion);rpc('flow.complete',$completion);
$finished=waitState($id,fn($s)=>$s['status']==='completed');
if($finished['steps']['fulfil']['status']!=='completed')throw new RuntimeException('Dependent step did not complete');
if(rpc('flow.cancel',$other)['status']!=='cancelled')throw new RuntimeException('Cancellation failed');
echo "PASS: authenticated queue start, scoped IDs, deferred callback/deduplication, dependent completion, cancellation\n";
