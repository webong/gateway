<?php

declare(strict_types=1);

use Gateway\Router\WorkermanLaravelHandler;
use Workerman\Connection\TcpConnection;
use Workerman\Protocols\Http\Request;
use Workerman\Worker;

$vendorDirectory = dirname(__DIR__).'/vendor';
$loader = require $vendorDirectory.'/autoload.php';
$loader->addPsr4('Gateway\\Router\\', __DIR__);

// The Workerman master starts from CLI, but each child is an HTTP worker.
putenv('APP_RUNNING_IN_CONSOLE=false');

$host = getenv('GATEWAY_PLANNER_HOST') ?: '0.0.0.0';
$port = (int) (getenv('GATEWAY_PLANNER_PORT') ?: 8080);
$workers = getenv('GATEWAY_PLANNER_WORKERS');
$workerCount = is_string($workers) && ctype_digit($workers) ? max(1, (int) $workers) : 4;
$dataDirectory = getenv('GATEWAY_ROUTER_DATA_DIR') ?: dirname(__DIR__).'/.gateway-router-data';

Worker::$logFile = rtrim($dataDirectory, '/').'/storage/logs/workerman.log';
Worker::$pidFile = rtrim($dataDirectory, '/').'/workerman.pid';

$handler = new WorkermanLaravelHandler(__DIR__);
$http = new Worker("http://{$host}:{$port}");
$http->name = 'gateway-planner';
$http->count = $workerCount;
$http->onMessage = static function (TcpConnection $connection, Request $request) use ($handler): void {
    $connection->send($handler->handle($request));
};
$http->onWorkerStop = static function () use ($handler): void {
    $handler->terminate();
};

Worker::runAll();
