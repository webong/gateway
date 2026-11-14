<?php
$runtime=require __DIR__.'/cutover-legacy-runtime.php';
$runtime['connection']->exec(file_get_contents('/extension/schema/sqlite.sql'));
Webong\WorkFlow\App\QueueScheduler::install($runtime['connection'],3);
foreach(glob('/state/*.json') as $path){chmod($path,0600);}
chown('/state',10001);chgrp('/state',10001);
foreach(glob('/state/*') as $path){chown($path,10001);chgrp($path,10001);}
