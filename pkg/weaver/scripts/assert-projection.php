<?php

declare(strict_types=1);

$root=dirname(__DIR__);
$package=$root.'/vendor/webong/workflow';
$marker=json_decode(file_get_contents($package.'/.source-plugin/projection.json'),true,128,JSON_THROW_ON_ERROR);
$expected=in_array('--compatibility-tests',$argv,true)?['core','open-workflow','temporal']:['core'];
$actual=$marker['surfaces']??[];sort($actual);sort($expected);
if (($marker['reference']??null)!=='278861082e00537fe7e759eb7c8bee3e7a3f0bf4' || $actual!==$expected) {
    throw new RuntimeException('Unexpected WorkFlow core version or projected surfaces.');
}
$installed=json_decode(file_get_contents($root.'/vendor/composer/installed.json'),true,128,JSON_THROW_ON_ERROR);
$core=null;
foreach($installed['packages'] as $candidate){if($candidate['name']==='webong/workflow'){$core=$candidate;break;}}
if (($core['installation-source']??null)!=='dist' || ($core['version']??null)!=='v0.1.0-rc.2') {
    throw new RuntimeException('WorkFlow must be installed from the actual released dist.');
}
foreach(['app/src/QueueScheduler.php','app/src/FlowRpcApplication.php','app/src/PdoFlowStateStore.php','app/cmd','mod'] as $excluded) {
    if (file_exists($package.'/'.$excluded)) {throw new RuntimeException('Original application/build source leaked into Weaver runtime closure.');}
}
if ($expected===['core']) {
    foreach(['ext/OpenWorkflow','ext/Temporal','ext/Laravel/Models'] as $excluded){
        if(file_exists($package.'/'.$excluded)){throw new RuntimeException('Unselected adapter leaked into production runtime.');}
    }
}
require_once $root.'/vendor/autoload.php';
if (!class_exists(Webong\Gateway\Weaver\QueueScheduler::class)
    || class_exists(Webong\WorkFlow\App\QueueScheduler::class)) {
    throw new RuntimeException('Weaver must own application runtime code independently of the projected engine.');
}
echo 'PASS released WorkFlow dist projection: '.implode(',',$actual)."\n";
