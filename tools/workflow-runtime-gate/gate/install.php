<?php
$runtime=require __DIR__.'/runtime.php';
$pdo=$runtime['connection'];
$pdo->exec(file_get_contents(dirname(__DIR__).'/schema/sqlite.sql'));
if (!$pdo->query("SELECT name FROM sqlite_master WHERE name='workflow_queue_lock'")->fetchColumn()) Webong\WorkFlow\App\QueueScheduler::install($pdo,1);
