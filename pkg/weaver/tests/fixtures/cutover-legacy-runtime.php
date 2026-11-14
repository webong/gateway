<?php
// Trusted rehearsal fixture only; never packaged in the production extension.
require_once '/extension/vendor/autoload.php';
spl_autoload_register(static function(string $class):void{
    $prefix='Webong\\WorkFlow\\App\\';
    if(str_starts_with($class,$prefix)){
        $path='/app/app/src/'.str_replace('\\','/',substr($class,strlen($prefix))).'.php';
        if(is_file($path)){require_once $path;}
    }
});
$name=getenv('CUTOVER_DATABASE')?:'source';
if(!in_array($name,['source','rollback'],true)){throw new RuntimeException('Unknown rehearsal database');}
$context=json_decode(file_get_contents('/state/runtime.json'),true,128,JSON_THROW_ON_ERROR);
$connection=new PDO('sqlite:/state/'.$name.'.sqlite',options:[PDO::ATTR_ERRMODE=>PDO::ERRMODE_EXCEPTION]);
$connection->exec('PRAGMA busy_timeout=5000');
$operations=new Webong\Gateway\Weaver\ApprovedOperations(Webong\Gateway\Weaver\ApprovedOperations::KEYS);
return [
    'connection'=>$connection,
    'definitions'=>new Webong\Gateway\Weaver\PublishedDefinitions($context['configuration']['accounts'][0]['definitions'],$operations),
    'stores'=>new Webong\WorkFlow\App\PdoSqliteFlowStateStoreFactory($connection,new Webong\WorkFlow\Services\DefaultFlowStateSerializer()),
    'queue'=>new Webong\WorkFlow\App\QueueScheduler($connection),'executors'=>[$operations],
];
