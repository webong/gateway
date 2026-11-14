<?php
// Disposable real Planner consumer: install the exact candidate Composer TAR.
$package=json_decode(file_get_contents('/release/composer-weaver-'.($argv[1]??'arm64').'.json'),true,128,JSON_THROW_ON_ERROR);
// Publication is separately approved: consume identical local bytes during CI.
$package['dist']['url']='file:///release/'.basename(parse_url($package['dist']['url'],PHP_URL_PATH));
$root='/gateway/app';chdir($root);
$composer=json_decode(file_get_contents('composer.json'),true,128,JSON_THROW_ON_ERROR);
$composer['require']['webong/gateway-weaver']=$package['version'];
$composer['repositories']=[['type'=>'package','package'=>$package],['type'=>'vcs','url'=>'https://github.com/webong/workflow','no-api'=>true]];
$composer['config']['allow-plugins']['webong/composer-source-plugin']=true;
$composer['extra']['source-plugin']['projections']['webong/workflow']=['surfaces'=>['core']];
file_put_contents('composer.json',json_encode($composer,JSON_PRETTY_PRINT|JSON_UNESCAPED_SLASHES|JSON_THROW_ON_ERROR));
foreach([
    'composer update webong/gateway-weaver --with-dependencies --no-plugins --no-dev --no-scripts --prefer-dist --no-interaction',
    'composer update webong/gateway-weaver --with-dependencies --no-dev --no-scripts --prefer-dist --no-interaction',
    'composer dump-autoload --no-dev --classmap-authoritative --no-scripts',
] as $command){passthru($command,$code);if($code!==0){exit($code);}}
$installed=json_decode(file_get_contents('/gateway/vendor/composer/installed.json'),true,128,JSON_THROW_ON_ERROR);
foreach($installed['packages'] as $candidate){if($candidate['name']==='webong/gateway-weaver'){$weaver=$candidate;break;}}
if(($weaver['installation-source']??null)!=='dist' || ($weaver['dist']['type']??null)!=='tar' || ($weaver['dist']['shasum']??null)!==$package['dist']['shasum']){
    throw new RuntimeException('Planner did not consume the candidate Composer dist.');
}
require '/gateway/vendor/autoload.php';
if(!class_exists(Webong\Gateway\Weaver\StandaloneCutover::class)){throw new RuntimeException('Candidate source missing');}
passthru('php /gateway/vendor/webong/gateway-weaver/bin/cutover',$usage);
if($usage!==2){throw new RuntimeException('Installed Composer CLI failed to bootstrap');}
passthru('su-exec www-data php /planner-smoke.php',$code);if($code!==0){exit($code);}
echo "PASS clean Planner consumer installed exact Composer TAR with authoritative autoload and approved core projection\n";
