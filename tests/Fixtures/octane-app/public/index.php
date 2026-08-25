<?php

declare(strict_types=1);

use Illuminate\Http\Request;

$root = dirname(__DIR__, 4);
$vendor = getenv('COMPOSER_VENDOR_DIR') ?: $root.'/vendor';

require $vendor.'/autoload.php';

$app = require dirname(__DIR__).'/bootstrap/app.php';
$app->handleRequest(Request::capture());
