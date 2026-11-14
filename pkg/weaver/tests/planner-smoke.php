<?php

declare(strict_types=1);

// Boot the production Planner with Weaver's Composer entrypoint; no testbench
// providers, host namespace edits, embedded HTTP server or production data.
require '/gateway/vendor/autoload.php';
putenv('APP_KEY=base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=');
@mkdir('/data/storage/framework/cache/data', 0770, true);
@mkdir('/data/storage/framework/sessions', 0770, true);
@mkdir('/data/storage/framework/views', 0770, true);
touch('/data/weaver-planner-test.sqlite');
$app = require '/gateway/app/planner/bootstrap/app.php';
$console = $app->make(Illuminate\Contracts\Console\Kernel::class);
$console->bootstrap();
$app['config']->set('app.key','base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=');
foreach ([
    ['migrate', ['--realpath'=>true, '--path'=>'/gateway/vendor/webong/proxy/database/migrations', '--force'=>true]],
    ['gateway:migrate', []], ['migrate', ['--force'=>true]],
] as [$command, $arguments]) {
    if ($console->call($command, $arguments)!==0) { throw new RuntimeException('Disposable Planner migrations failed.'); }
}
$app['config']->set('gateway.registry_token', 'disposable-weaver-planner-operator');
$accounts = $app->make(Webong\Gateway\Laravel\Accounts\AccountStore::class);
$accountToken = $accounts->create('account-a');
$accounts->create('account-b');
$http = $app->make(Illuminate\Contracts\Http\Kernel::class);

function check(bool $condition, string $message): void {
    if (!$condition) { throw new RuntimeException($message); }
}
function callPlanner(string $method, string $path, array $body=[], ?string $token='disposable-weaver-planner-operator', ?int $revision=null): Symfony\Component\HttpFoundation\Response {
    global $http;
    $server=['CONTENT_TYPE'=>'application/json', 'HTTP_ACCEPT'=>'application/json'];
    if($token!==null){$server['HTTP_AUTHORIZATION']='Bearer '.$token;}
    if($revision!==null){$server['HTTP_IF_MATCH']='"'.$revision.'"';}
    $request=Illuminate\Http\Request::create($path,$method,server:$server,content:json_encode($body,JSON_THROW_ON_ERROR));
    $response=$http->handle($request);$http->terminate($request,$response);
    return $response;
}
function document(Symfony\Component\HttpFoundation\Response $response): array {
    return json_decode($response->getContent(),true,128,JSON_THROW_ON_ERROR);
}
$path='/weaver/accounts/account-a';
check(callPlanner('GET',$path.'/definitions',token:null)->getStatusCode()===401,'Anonymous publication access accepted');
check(callPlanner('GET',$path.'/definitions',token:$accountToken)->getStatusCode()===401,'Account ownership granted management rights');
$definition=['key'=>'order','version'=>1,'steps'=>[
    ['id'=>'approve','label'=>'Approve','metadata'=>['operation'=>'gateway.approval']],
    ['id'=>'fulfil','depends_on'=>['approve'],'metadata'=>['operation'=>'gateway.complete']],
]];
$response=callPlanner('POST',$path.'/definitions',$definition);
check($response->getStatusCode()===201,'Definition publication failed: '.$response->getContent());
$first=document($response);
check(document(callPlanner('POST',$path.'/definitions',$definition))['id']===$first['id'],'Idempotent publication changed identity');
$changed=$definition;$changed['steps'][0]['label']='Changed';
check(callPlanner('POST',$path.'/definitions',$changed)->getStatusCode()===409,'Published definition mutated in place');
$response=callPlanner('POST',$path.'/definitions',[...$definition,'version'=>2]);
check($response->getStatusCode()===201,'New definition version rejected');
$second=document($response);
$other=document(callPlanner('POST','/weaver/accounts/account-b/definitions',$definition));
$grant=['flow_key'=>'order','subject'=>['type'=>'order','id'=>'42'],'actions'=>['flow.start','flow.get','flow.complete','flow.cancel']];
check(callPlanner('POST',$path.'/grants',[...$grant,'account'=>'account-b'])->getStatusCode()===422,'Grant body replaced route ownership');
$response=callPlanner('POST',$path.'/grants',$grant);
check($response->getStatusCode()===201,'Grant creation failed: '.$response->getContent());
$created=document($response);
check(callPlanner('PUT',$path.'/grants/'.$created['id'],$grant)->getStatusCode()===428,'Grant update omitted If-Match');
check(callPlanner('PUT',$path.'/grants/'.$created['id'],$grant,revision:99)->getStatusCode()===412,'Stale grant revision accepted');
$resources=$app->make(Webong\Gateway\Weaver\Planner\WeaverResources::class);
$registry=$app->make(Webong\Gateway\Companion\ExtensionRuntimeConfigurationRegistry::class);
$base=['schema_version'=>1,'database_ref'=>'database','authentication_ref'=>'authentication','operations'=>['gateway.approval','gateway.complete'],
    'accounts'=>[['id'=>'account-a','definitions'=>[$first['id']]]]];
$projected=$registry->project('webong/gateway-weaver',$base);
check($projected['accounts'][0]['definitions'][0]['version']===1,'Selected immutable version drifted');
check(count($projected['accounts'][0]['grants'])===1,'Explicit grant missing from projection');
check(!array_key_exists('database',$projected) && !array_key_exists('authentication',$projected),'Credentials entered desired state');
$cross=$base;$cross['accounts'][0]['definitions']=[$other['id']];
check($registry->project('webong/gateway-weaver',$cross)===['schema_version'=>0],'Cross-account definition selection did not fail closed');
$ambiguous=$base;$ambiguous['accounts'][0]['definitions']=[$first['id'],$second['id']];
check($registry->project('webong/gateway-weaver',$ambiguous)===['schema_version'=>0],'Ambiguous active versions accepted');
$changedGrant=[...$grant,'actions'=>['flow.get']];
$response=callPlanner('PUT',$path.'/grants/'.$created['id'],$changedGrant,revision:1);
check($response->getStatusCode()===200 && $response->headers->get('ETag')==='"2"','Grant replacement revision not preserved');
$changedProjection=$registry->project('webong/gateway-weaver',$base);
check($changedProjection['accounts'][0]['grants'][0]['actions']===['flow.get'],'Grant replacement not projected');
check(callPlanner('DELETE','/weaver/accounts/account-b/grants/'.$created['id'],revision:2)->getStatusCode()===404,'Cross-account grant mutation accepted');
check(callPlanner('DELETE',$path.'/grants/'.$created['id'],revision:2)->getStatusCode()===204,'Grant revocation failed');
check($registry->project('webong/gateway-weaver',$base)['accounts'][0]['grants']===[],'Revoked grant remained active');
$kinds=$app->make(Webong\Gateway\Resources\ConfigurationKindRegistry::class)->all();
check(in_array('weaver.definition',$kinds,true) && in_array('weaver.grant',$kinds,true),'Resource kinds were not registered');
echo "PASS Planner entrypoint: real application/provider registry, immutable versions, explicit grants, account isolation, conditional revisions, revocation and fail-closed projection\n";
