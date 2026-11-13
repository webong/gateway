<?php
$files=[];
$iterator=new RecursiveIteratorIterator(new RecursiveDirectoryIterator('/package',FilesystemIterator::SKIP_DOTS));
foreach($iterator as $f){if($f->isFile())$files[]=substr($f->getPathname(),9);}
sort($files);
$artifacts=[];$spinner=null;
foreach($files as $i=>$path){$name='file-'.$i;$artifacts[]=['name'=>$name,'path'=>$path,'sha256'=>hash_file('sha256','/package/'.$path)];if($path==='bin/spinner')$spinner=$name;}
$manifest=[
 'apiVersion'=>'ext.package/v1',
 'descriptor'=>['apiVersion'=>'ext.plugin/v1','identity'=>['id'=>'webong/gateway-workflow','revision'=>'runtime-gate-00e0bbc','version'=>'0.0.0-gate'],
 'contracts'=>[['name'=>'gateway.runtime','version'=>'v1','operations'=>[['name'=>'start','surface'=>'spinner'],['name'=>'stop','surface'=>'spinner']]]],
 'reverse'=>[['name'=>'gateway.secret','version'=>'v1','operations'=>[['name'=>'resolve']]]]],
 'metadata'=>['gateway.spinner/v1'=>['platform'=>['os'=>'linux','arch'=>getenv('TARGETARCH')]],'gateway.runtime/v1'=>['target'=>'spinner'],'workflow.runtime-gate/v1'=>['source'=>'00e0bbcdd61c2c6ed96af55865d0ef2e77557adb','php'=>'8.3','pdo'=>['sqlite','pgsql','mysql'],'status'=>'prototype']],
 'artifacts'=>$artifacts,'entrypoints'=>[['name'=>'spinner','runtime'=>'process','artifact'=>$spinner,'protocols'=>['ext.plugin/v1']]],'requires'=>[],
];
file_put_contents('/package/plugin.json',json_encode($manifest,JSON_PRETTY_PRINT|JSON_UNESCAPED_SLASHES|JSON_THROW_ON_ERROR)."\n");
