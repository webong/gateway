<?php
$runtime=require __DIR__.'/runtime.php';
$worker=new Webong\WorkFlow\App\QueueWorker($runtime['queue'],$runtime['executors']);
$running=true;
pcntl_async_signals(true);
pcntl_signal(SIGTERM,static function()use(&$running){$running=false;});
pcntl_signal(SIGINT,static function()use(&$running){$running=false;});
@unlink('/state/pause-worker');@unlink('/state/worker-paused');
file_put_contents('/state/worker.pid',(string)getmypid());
fwrite(STDOUT,"READY\n");fflush(STDOUT);
while($running){
 if(is_file('/state/pause-worker')){file_put_contents('/state/worker-paused','1');usleep(100000);continue;}
 if(!$worker->tick(2))usleep(100000);
}
