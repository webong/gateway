<?php
// Trusted disposable setup through production stores; not a public setup route.
use Webong\Gateway\Laravel\Companion\{CompanionActionActivationStore,ExtensionSecretStore};
$app=require '/gateway/app/planner/bootstrap/app.php';
$app->make(Illuminate\Contracts\Console\Kernel::class)->bootstrap();
$store=$app->make(CompanionActionActivationStore::class);
$identity='webong/gateway-weaver';
$mode=$argv[1]??'activate';
$version=getenv('WORKFLOW_GATE_EXTENSION_VERSION')?:'0.1.0-dev';
$providerToken=getenv('WORKFLOW_GATE_PROVIDER_TOKEN')?:'disposable-weaver-provider-token';
$accountToken=getenv('WORKFLOW_GATE_ACCOUNT_TOKEN')?:'disposable-weaver-runtime-gate-token';
$configuration=json_decode(file_get_contents('/gate/weaver.configuration.json'),true,128,JSON_THROW_ON_ERROR);
// Seed only desired resources through the installed Composer provider; the
// runtime snapshot must expand owned published IDs and current grants.
$accounts=$app->make(Webong\Gateway\Laravel\Accounts\AccountStore::class);
$resources=$app->make(Webong\Gateway\Weaver\Planner\WeaverResources::class);
foreach($configuration['accounts'] as &$account){
 if(!$accounts->exists($account['id']))$accounts->create($account['id']);
 $ids=[];
 foreach($account['definitions'] as $definition)$ids[]=$resources->publish($account['id'],$definition)->id;
 foreach($account['grants'] as $grant){unset($grant['account']);$resources->createGrant($account['id'],$grant);}
 $account=['id'=>$account['id'],'definitions'=>$ids];
}
unset($account);
$row=$store->byExtension($identity);
if(in_array($mode,['activate','invalid-config'],true)){
 if($mode==='invalid-config'){$configuration['accounts'][0]['definitions']=['missing-published-definition'];}
 $input=['extension'=>$identity,'enabled'=>true,'configuration'=>$configuration,'version'=>$version,'artifact'=>['url'=>'https://extensions.invalid/gateway-workflow.tar.gz','sha256'=>getenv('WORKFLOW_GATE_ARTIFACT_SHA')]];
 if($row===null)$row=$store->create($input);
 else $row=$store->replace($row->id,$input,$row->revision);
 $secrets=$app->make(ExtensionSecretStore::class);
 foreach(['weaver-database'=>['dsn'=>'sqlite:/state/workflow.sqlite'],'weaver-authentication'=>['gate-account'=>$accountToken],'weaver-http-orders'=>['url'=>'http://weaver-provider:8089/fulfil','effect_scope'=>'provider-account-1','headers'=>['Authorization'=>['Bearer '.$providerToken]],'idempotent'=>true,'timeout_seconds'=>1],'weaver-http-unsafe'=>['url'=>'http://weaver-provider:8089/fulfil','effect_scope'=>'provider-account-1','headers'=>['Authorization'=>['Bearer '.$providerToken]],'idempotent'=>false,'timeout_seconds'=>1]] as $reference=>$value){
  $old=$secrets->metadata($row->id,$reference);
  $secrets->put($row->id,$reference,$value,$old['version']??null);
 if(in_array($mode,['unavailable-storage','changed-http-scope'],true)){$store->replace($row->id,['extension'=>$identity,'enabled'=>true,'configuration'=>$configuration,'version'=>$version,'artifact'=>['url'=>'https://extensions.invalid/gateway-workflow.tar.gz','sha256'=>getenv('WORKFLOW_GATE_ARTIFACT_SHA')]],$row->revision);}
 }
}elseif(in_array($mode,['rotate-secret','unavailable-storage','changed-http-scope'],true)){
 if($row===null)throw new RuntimeException('Activation missing');
 $secrets=$app->make(ExtensionSecretStore::class);
 $reference=match($mode){'rotate-secret'=>'weaver-authentication','changed-http-scope'=>'weaver-http-orders',default=>'weaver-database'};
 $old=$secrets->metadata($row->id,$reference);
 $value=match($mode){'rotate-secret'=>['gate-account'=>'disposable-weaver-runtime-gate-token-rotated'],'changed-http-scope'=>['url'=>'http://weaver-provider:8089/fulfil','effect_scope'=>'provider-account-2','headers'=>['Authorization'=>['Bearer disposable-weaver-provider-other-account']],'idempotent'=>true,'timeout_seconds'=>1],default=>['dsn'=>'sqlite:/missing-parent/workflow.sqlite']};
 $secrets->put($row->id,$reference,$value,$old['version']??null);
 if(in_array($mode,['unavailable-storage','changed-http-scope'],true)){$store->replace($row->id,['extension'=>$identity,'enabled'=>true,'configuration'=>$configuration,'version'=>$version,'artifact'=>['url'=>'https://extensions.invalid/gateway-workflow.tar.gz','sha256'=>getenv('WORKFLOW_GATE_ARTIFACT_SHA')]],$row->revision);}
}elseif($mode==='revoke-grants'){
 foreach($resources->grants('gate-account') as $grant){$resources->revokeGrant('gate-account',$grant->id,$grant->revision);}
}elseif($mode==='disable'){
 if($row===null)throw new RuntimeException('Activation missing');
 $store->replace($row->id,['extension'=>$identity,'enabled'=>false,'version'=>$version,'artifact'=>['url'=>'https://extensions.invalid/gateway-workflow.tar.gz','sha256'=>getenv('WORKFLOW_GATE_ARTIFACT_SHA')]],$row->revision);
}elseif($mode==='remove-secret'){
 if($row===null)throw new RuntimeException('Activation missing');
 $secrets=$app->make(ExtensionSecretStore::class);
 $old=$secrets->metadata($row->id,'weaver-authentication');
 if($old!==null)$secrets->delete($row->id,'weaver-authentication',$old['version']);
}else{throw new InvalidArgumentException('Unknown fixture mode');}
echo "Disposable Planner activation operation completed.\n";
