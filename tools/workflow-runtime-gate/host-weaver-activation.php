<?php
// Trusted disposable setup through production stores; not a public setup route.
use Webong\Gateway\Laravel\Companion\{CompanionActionActivationStore,ExtensionSecretStore};
$app=require '/gateway/app/bootstrap.php';
$app->make(Illuminate\Contracts\Console\Kernel::class)->bootstrap();
$store=$app->make(CompanionActionActivationStore::class);
$identity='webong/gateway-weaver';
$configuration=json_decode(file_get_contents('/gate/weaver.configuration.json'),true,128,JSON_THROW_ON_ERROR);
$row=$store->byExtension($identity);
$mode=$argv[1]??'activate';
if($mode==='activate'){
 $input=['extension'=>$identity,'enabled'=>true,'configuration'=>$configuration,'version'=>'0.1.0-dev','artifact'=>['url'=>'https://extensions.invalid/gateway-workflow.tar.gz','sha256'=>getenv('WORKFLOW_GATE_ARTIFACT_SHA')]];
 if($row===null)$row=$store->create($input);
 else $row=$store->replace($row->id,$input,$row->revision);
 $secrets=$app->make(ExtensionSecretStore::class);
 foreach(['weaver-database'=>['dsn'=>'sqlite:/state/workflow.sqlite'],'weaver-authentication'=>['gate-account'=>'disposable-weaver-runtime-gate-token']] as $reference=>$value){
  $old=$secrets->metadata($row->id,$reference);
  $secrets->put($row->id,$reference,$value,$old['version']??null);
 }
}elseif($mode==='disable'){
 if($row===null)throw new RuntimeException('Activation missing');
 $store->replace($row->id,['extension'=>$identity,'enabled'=>false,'version'=>'0.1.0-dev','artifact'=>['url'=>'https://extensions.invalid/gateway-workflow.tar.gz','sha256'=>getenv('WORKFLOW_GATE_ARTIFACT_SHA')]],$row->revision);
}elseif($mode==='remove-secret'){
 if($row===null)throw new RuntimeException('Activation missing');
 $secrets=$app->make(ExtensionSecretStore::class);
 $old=$secrets->metadata($row->id,'weaver-authentication');
 if($old!==null)$secrets->delete($row->id,'weaver-authentication',$old['version']);
}else{throw new InvalidArgumentException('Unknown fixture mode');}
echo "Disposable Planner activation operation completed.\n";
