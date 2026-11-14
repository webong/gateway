<?php
// Build-time ELF closure. Never run on activation and never inspect host PATH.
$root='/package';
mkdir($root.'/runtime/lib',0755,true);mkdir($root.'/runtime/bin',0755,true);mkdir($root.'/runtime/php.d',0755,true);
copy('/usr/local/bin/php',$root.'/runtime/bin/php');copy('/usr/local/bin/workflowd',$root.'/runtime/bin/workflowd');
$extensions=glob(ini_get('extension_dir').'/*.so');
$inputs=['/usr/local/bin/php','/usr/local/bin/workflowd',...$extensions];
foreach($extensions as $path)copy($path,$root.'/runtime/lib/'.basename($path));
foreach(glob('/usr/local/etc/php/conf.d/*.ini') as $path)copy($path,$root.'/runtime/php.d/'.basename($path));
foreach($inputs as $path){
 exec('ldd '.escapeshellarg($path).' 2>&1',$lines,$code);
 if($code!==0)throw new RuntimeException('Cannot inspect ELF closure: '.$path);
 foreach($lines as $line){
  if(str_contains($line,'not found'))throw new RuntimeException('Unresolved runtime library');
  if(preg_match('~(?:=>\s+)?(/[^\s]+)~',$line,$m)){
   $source=$m[1];$target=$root.'/runtime/lib/'.basename($source);
   if(is_file($target)&&hash_file('sha256',$target)!==hash_file('sha256',$source))throw new RuntimeException('Runtime library basename collision');
   if(!is_file($target))copy($source,$target);
   if(str_contains(basename($source),'ld-linux'))copy($source,$root.'/runtime/loader');
  }
 }
 $lines=[];
}
if(!is_file($root.'/runtime/loader'))throw new RuntimeException('ELF loader missing');
chmod($root.'/runtime/loader',0755);chmod($root.'/runtime/bin/php',0755);chmod($root.'/runtime/bin/workflowd',0755);
file_put_contents($root.'/php.ini',"extension_dir=/package/runtime/lib\nlog_errors=On\ndisplay_errors=Off\n");
