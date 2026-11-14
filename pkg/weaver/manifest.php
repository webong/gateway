<?php
$version=getenv('WEAVER_VERSION')?:'0.1.0-dev';
if(preg_match('/\A[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?\z/D',$version)!==1){throw new RuntimeException('Invalid Weaver version');}
$files=[];
$iterator=new RecursiveIteratorIterator(new RecursiveDirectoryIterator('/package',FilesystemIterator::SKIP_DOTS));
foreach($iterator as $f){if($f->isFile())$files[]=substr($f->getPathname(),9);}
sort($files);
$artifacts=[];$spinner=null;
foreach($files as $i=>$path){$name='file-'.$i;$artifacts[]=['name'=>$name,'path'=>$path,'sha256'=>hash_file('sha256','/package/'.$path)];if($path==='bin/spinner')$spinner=$name;}
$manifest=[
 'apiVersion'=>'ext.package/v1',
 'descriptor'=>['apiVersion'=>'ext.plugin/v1','identity'=>['id'=>'webong/gateway-weaver','revision'=>getenv('WEAVER_REVISION'),'version'=>$version],
 'contracts'=>[['name'=>'gateway.runtime','version'=>'v1','operations'=>[['name'=>'start','surface'=>'spinner'],['name'=>'stop','surface'=>'spinner']]],['name'=>'gateway.delivery-adapter','version'=>'v1','operations'=>[['name'=>'deliver','surface'=>'spinner']]]],
 'reverse'=>[['name'=>'gateway.secret','version'=>'v1','operations'=>[['name'=>'resolve']]],['name'=>'gateway.delivery','version'=>'v1','operations'=>[['name'=>'deliver-pinned-http'],['name'=>'supports-pinned-http'],['name'=>'durable-idempotent'],['name'=>'own-adapter-ready'],['name'=>'enqueue-gateway-idempotent']]]]],
 'metadata'=>['gateway.spinner/v1'=>['platform'=>['os'=>'linux','arch'=>getenv('TARGETARCH')]],'gateway.runtime/v1'=>['target'=>'spinner'],'gateway.planner/v1'=>['composer'=>'webong/gateway-weaver','provider'=>'Webong\\Gateway\\Weaver\\Planner\\WeaverServiceProvider','version'=>$version],'gateway.weaver/v1'=>['source'=>'278861082e00537fe7e759eb7c8bee3e7a3f0bf4','runtimeImage'=>getenv('WEAVER_RUNTIME_IMAGE'),'bridgeBuilderImage'=>'dunglas/frankenphp:1.12.7-builder-php8.3-bookworm@sha256:668b927010b6ce7a623e70c9cd6a4ba91a4763184e3c506596c828a07d0560db','coreVersion'=>'v0.1.0-rc.2','surfaces'=>['core'],'php'=>'8.3','pdo'=>['sqlite','pgsql','mysql'],'status'=>'development','schema'=>1]],
 'artifacts'=>$artifacts,'entrypoints'=>[['name'=>'spinner','runtime'=>'process','artifact'=>$spinner,'protocols'=>['ext.plugin/v1']]],'requires'=>[],
];
file_put_contents('/package/plugin.json',json_encode($manifest,JSON_PRETTY_PRINT|JSON_UNESCAPED_SLASHES|JSON_THROW_ON_ERROR)."\n");
