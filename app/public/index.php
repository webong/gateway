<?php

declare(strict_types=1);

use Illuminate\Http\Request;

require dirname(__DIR__).'/vendor/autoload.php';

$app = require dirname(__DIR__).'/bootstrap.php';
$app->handleRequest(Request::capture());
