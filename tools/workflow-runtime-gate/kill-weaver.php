<?php
$root=getenv('WEAVER_PACKAGE_ROOT');$pids=[];
foreach(glob('/proc/[0-9]*/cmdline') as $path){
 $args=explode("\0",file_get_contents($path));
 if(($args[0]??null)===$root.'/bin/spinner' && count(array_filter($args,static fn($s)=>$s!==''))===1)$pids[]=(int)basename(dirname($path));
}
if(count($pids)!==1 || !posix_kill($pids[0],SIGKILL))throw new RuntimeException('Expected one Weaver guest');
echo "Terminated Weaver guest for abrupt-parent-death acceptance.\n";
