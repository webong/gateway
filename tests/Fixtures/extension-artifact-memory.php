<?php
// Separate process: the installer's memory limit is independent of Pest's heap.
declare(strict_types=1);

require $argv[1];
$archive=$argv[2];$store=$argv[3];
$url='https://extensions.example.test/large-runtime.tar.gz';
$http=new Illuminate\Http\Client\Factory;
$http->fake([$url=>Illuminate\Http\Client\Factory::response(fopen($archive,'rb'),200)]);
$installer=new Webong\Gateway\Laravel\Companion\ExtensionArtifactInstaller($store,$http);
$installer->install((object)[
    'key'=>'example/large-runtime','revision'=>1,
    'document'=>['package'=>['version'=>'1.0.0','artifact'=>['url'=>$url,'sha256'=>hash_file('sha256',$archive)]]],
]);
if(filesize($store.'/example%2flarge-runtime/runtime.bin')!==80*1024*1024){
    throw new RuntimeException('Large runtime was not fully extracted');
}
echo "PASS 80 MiB runtime installed under a 64 MiB PHP memory limit\n";
