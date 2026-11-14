<?php
$root=getenv('WEAVER_PACKAGE_ROOT');
require_once $root.'/vendor/autoload.php';
$connection=new PDO('sqlite:/state/workflow.sqlite',options:[PDO::ATTR_ERRMODE=>PDO::ERRMODE_EXCEPTION]);
$connection->exec('PRAGMA busy_timeout=5000');
return ['connection'=>$connection,'queue'=>new Webong\Gateway\Weaver\QueueScheduler($connection)];
