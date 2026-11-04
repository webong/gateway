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
$requests = getenv('GATEWAY_PLANNER_MAX_REQUESTS');
$maxRequests = is_string($requests) && ctype_digit($requests) ? max(1, (int) $requests) : 500;
$dataDirectory = getenv('GATEWAY_ROUTER_DATA_DIR') ?: dirname(__DIR__).'/.gateway-router-data';

Worker::$logFile = rtrim($dataDirectory, '/').'/storage/logs/workerman.log';
Worker::$pidFile = rtrim($dataDirectory, '/').'/workerman.pid';

$handler = null;
$handledRequests = 0;
$http = new Worker("http://{$host}:{$port}");
$http->name = 'gateway-planner';
$http->count = $workerCount;
$http->onWorkerStart = static function () use (&$handler, &$handledRequests): void {
    $handler = new WorkermanLaravelHandler(__DIR__);
    $handledRequests = 0;
};
$http->onMessage = static function (TcpConnection $connection, Request $request) use (&$handler, &$handledRequests, $maxRequests, $http): void {
    if (! $handler instanceof WorkermanLaravelHandler) {
        $connection->send(new Workerman\Protocols\Http\Response(503, ['Content-Type' => 'text/plain'], 'Service Unavailable'));

        return;
    }
    $connection->send($handler->handle($request));
    if (++$handledRequests >= $maxRequests) {
        Worker::stopAll();
    }
};
$http->onWorkerStop = static function () use (&$handler): void {
    $handler?->terminate();
    $handler = null;
};

Worker::runAll();
