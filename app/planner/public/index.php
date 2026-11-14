<?php

declare(strict_types=1);

use Illuminate\Http\Request;

$app = require dirname(__DIR__).'/bootstrap/app.php';

$app->handleRequest(Request::capture());
