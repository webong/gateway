<?php
// Pinned original app classes; never a production dependency or copied into Weaver.
require_once '/extension/vendor/autoload.php';
spl_autoload_register(static function(string $class):void{
 $prefix='Webong\\WorkFlow\\App\\';
 if(str_starts_with($class,$prefix)){
  $path='/app/app/src/'.str_replace('\\','/',substr($class,strlen($prefix))).'.php';
  if(is_file($path))require_once $path;
 }
});
$name=getenv('OPERATOR_DATABASE')?:'legacy';
if(!in_array($name,['legacy','backup-restored','reference','rollback'],true))throw new RuntimeException('Unknown operator fixture database');
$config=require '/gate/operator-configuration.php';
$definition=$config['accounts'][0]['definitions'][0];$definition['version']=1;$definition['steps'][2]['id']='finish-v1';
$pdo=new PDO('sqlite:/state/'.$name.'.sqlite',options:[PDO::ATTR_ERRMODE=>PDO::ERRMODE_EXCEPTION]);
$pdo->exec('PRAGMA busy_timeout=5000');
$operations=new Webong\Gateway\Weaver\ApprovedOperations(Webong\Gateway\Weaver\ApprovedOperations::KEYS);
return ['connection'=>$pdo,'definitions'=>new Webong\Gateway\Weaver\PublishedDefinitions([$definition],$operations),
 'stores'=>new Webong\WorkFlow\App\PdoSqliteFlowStateStoreFactory($pdo,new Webong\WorkFlow\Services\DefaultFlowStateSerializer()),
 'queue'=>new Webong\WorkFlow\App\QueueScheduler($pdo),'executors'=>[$operations]];
