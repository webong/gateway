<?php
// Trusted disposable setup through production stores; not a public setup route.
use Webong\Gateway\Laravel\Companion\{CompanionActionActivationStore,ExtensionSecretStore};
$app=require '/gateway/app/planner/bootstrap/app.php';
$app->make(Illuminate\Contracts\Console\Kernel::class)->bootstrap();
$store=$app->make(CompanionActionActivationStore::class);
$identity='webong/gateway-weaver';
$row=$store->byExtension($identity);
$mode=$argv[1]??'activate';
if($mode==='activate'){
 $input=['extension'=>$identity,'enabled'=>true,'version'=>'0.0.0-gate','artifact'=>['url'=>'https://extensions.invalid/gateway-workflow.tar.gz','sha256'=>getenv('WORKFLOW_GATE_ARTIFACT_SHA')]];
 if($row===null)$row=$store->create($input);
 else $row=$store->replace($row->id,$input,$row->revision);
 $secrets=$app->make(ExtensionSecretStore::class);
 $old=$secrets->metadata($row->id,'workflow-runtime');
 $secrets->put($row->id,'workflow-runtime',['rpcToken'=>'disposable-weaver-runtime-gate-token'],$old['version']??null);
}elseif($mode==='disable'){
 if($row===null)throw new RuntimeException('Activation missing');
 $store->replace($row->id,['extension'=>$identity,'enabled'=>false,'version'=>'0.0.0-gate','artifact'=>['url'=>'https://extensions.invalid/gateway-workflow.tar.gz','sha256'=>getenv('WORKFLOW_GATE_ARTIFACT_SHA')]],$row->revision);
}elseif($mode==='remove-secret'){
 if($row===null)throw new RuntimeException('Activation missing');
 $secrets=$app->make(ExtensionSecretStore::class);
 $old=$secrets->metadata($row->id,'workflow-runtime');
 if($old!==null)$secrets->delete($row->id,'workflow-runtime',$old['version']);
}else{throw new InvalidArgumentException('Unknown fixture mode');}
echo "Disposable Planner activation operation completed.\n";
