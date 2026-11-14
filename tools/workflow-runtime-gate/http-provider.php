<?php
// Disposable external HTTP-provider fixture. Commits one effect, then loses its
// first response; the next request must reuse its provider idempotency receipt.
if(parse_url($_SERVER['REQUEST_URI'],PHP_URL_PATH)==='/healthz'){http_response_code(204);exit;}
$scope=match($_SERVER['HTTP_AUTHORIZATION']??null){'Bearer disposable-weaver-provider-token','Bearer disposable-weaver-provider-token-rotated'=>'provider-account-1','Bearer disposable-weaver-provider-other-account'=>'provider-account-2',default=>null};
if($scope===null){http_response_code(401);exit;}
$key=$_SERVER['HTTP_IDEMPOTENCY_KEY']??$_SERVER['HTTP_X_WEAVER_EFFECT_KEY']??'';
if(preg_match('/\A[a-f0-9]{64}\z/D',$key)!==1){http_response_code(400);exit;}
$body=file_get_contents('php://input');
$connection=new PDO('sqlite:/state/provider-effects.sqlite',options:[PDO::ATTR_ERRMODE=>PDO::ERRMODE_EXCEPTION]);
$connection->exec('PRAGMA busy_timeout=5000');
$connection->exec('CREATE TABLE IF NOT EXISTS effects (scope TEXT NOT NULL, receipt TEXT NOT NULL, requests INTEGER NOT NULL, payload TEXT NOT NULL, PRIMARY KEY(scope,receipt))');
$statement=$connection->prepare('SELECT payload FROM effects WHERE scope=? AND receipt=?');$statement->execute([$scope,$key]);$existing=$statement->fetchColumn();
if($existing!==false && $existing!==$body){http_response_code(409);exit;}
if($existing===false){
 $connection->prepare('INSERT INTO effects VALUES (?,?,1,?)')->execute([$scope,$key,$body]);
 // No complete response: Gateway must report an uncertain external outcome.
 http_response_code(200);header('Content-Length: 100');echo '';exit;
}
$connection->prepare('UPDATE effects SET requests=requests+1 WHERE scope=? AND receipt=?')->execute([$scope,$key]);
http_response_code(204);
