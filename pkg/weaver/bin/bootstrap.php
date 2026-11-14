<?php

declare(strict_types=1);

// Native artifact or root Composer checkout; otherwise a Composer dependency
// installed under the consumer's vendor directory (including custom vendor-dir).
$autoload=dirname(__DIR__).'/vendor/autoload.php';
if(!is_file($autoload)){$autoload=dirname(__DIR__,3).'/autoload.php';}
if(!is_file($autoload)){throw new RuntimeException('Weaver Composer autoload is unavailable.');}
require_once $autoload;
