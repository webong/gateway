<?php

declare(strict_types=1);

use Gateway\Router\Workerman\LaravelHandler;
use Workerman\Connection\TcpConnection;
use Workerman\Protocols\Http\Request;
use Workerman\Timer;
use Workerman\Worker;

// app/servers/workerman/server.php: the Planner host is app/planner and the
// shared Composer vendor directory sits beside app/.
$appDirectory = dirname(__DIR__, 2);
$vendorDirectory = dirname($appDirectory).'/vendor';
$loader = require $vendorDirectory.'/autoload.php';
$loader->addPsr4('Gateway\\Router\\', $appDirectory.'/helpers');

// The Workerman master starts from CLI, but each child is an HTTP worker.
putenv('APP_RUNNING_IN_CONSOLE=false');

$host = getenv('GATEWAY_PLANNER_HOST') ?: '0.0.0.0';
$port = (int) (getenv('GATEWAY_PLANNER_PORT') ?: 8080);
$workers = getenv('GATEWAY_PLANNER_WORKERS');
$workerCount = is_string($workers) && ctype_digit($workers) ? max(1, (int) $workers) : 4;
$requests = getenv('GATEWAY_PLANNER_MAX_REQUESTS');
$maxRequests = is_string($requests) && ctype_digit($requests) ? max(1, (int) $requests) : 500;
$dataDirectory = getenv('GATEWAY_ROUTER_DATA_DIR') ?: dirname($appDirectory).'/.gateway-router-data';

Worker::$logFile = rtrim($dataDirectory, '/').'/storage/logs/workerman.log';
Worker::$pidFile = rtrim($dataDirectory, '/').'/workerman.pid';

// How long a worker that reached its request limit keeps serving the connections it already holds.
$drainSeconds = 2.0;

$handler = null;
$handledRequests = 0;
$draining = false;
$stopping = false;
$http = new Worker("http://{$host}:{$port}");
$http->name = 'gateway-planner';
$http->count = $workerCount;
$http->onWorkerStart = static function () use (&$handler, &$handledRequests, $appDirectory): void {
    $handler = new LaravelHandler($appDirectory.'/planner');
    $handledRequests = 0;
};
// Recycling a worker must not cost a client a request. Workerman keeps a client's connection in
// the worker that accepted it, and stopping a worker closes every connection it holds: a client
// that reuses one of them, or whose next request was already sent, gets an empty reply.
//
// So a worker that reached its limit drains first, the way nginx retires a connection after
// `keepalive_requests`: it stops accepting (the master keeps the listening socket, so other
// workers or the replacement take new connections), answers each remaining request with
// `Connection: close`, and stops once its connections are gone. Only a connection that stays
// silent for the whole drain window, whose client never learned to leave, is closed idle.
$startDraining = static function () use (&$draining, &$stopping, $http, $drainSeconds): void {
    if ($draining) {
        return;
    }
    $draining = true;
    $http->unlisten();

    $deadline = microtime(true) + $drainSeconds;
    $timer = 0;
    $timer = Timer::add(0.01, static function () use (&$timer, &$stopping, $http, $deadline): void {
        if (! $stopping && $http->connections !== [] && microtime(true) < $deadline) {
            return;
        }
        Timer::del($timer);
        if (! $stopping) {
            Worker::stopAll();
        }
    });
};
$http->onMessage = static function (TcpConnection $connection, Request $request) use (&$handler, &$handledRequests, &$draining, $maxRequests, $startDraining): void {
    if (! $handler instanceof LaravelHandler) {
        $connection->send(new Workerman\Protocols\Http\Response(503, ['Content-Type' => 'text/plain'], 'Service Unavailable'));

        return;
    }
    $response = $handler->handle($request);
    if (++$handledRequests >= $maxRequests) {
        $startDraining();
    }
    if ($draining) {
        // end() flushes the reply, then half-closes and drains what the client still sends, so the
        // reply is not lost to a reset when a pipelined request arrives behind it.
        $connection->end($response->withHeader('Connection', 'close'));

        return;
    }
    $connection->send($response);
};
$http->onWorkerStop = static function (Worker $worker) use (&$handler, &$stopping): void {
    $stopping = true;
    $handler?->terminate();
    $handler = null;

    // On SIGQUIT (graceful stop) Workerman stops accepting and then waits until every
    // connection has closed, and it never closes one itself: not an idle keep-alive
    // connection, and not even the one a response was just written to. A client that
    // keeps its connection pool open would hold this worker, the master and the container
    // until the orchestrator's SIGKILL.
    //
    // No request is being handled here (the stop is processed between events, after a
    // response has been written), so everything still open is idle or finished. Close it;
    // a connection whose response is still buffered is closed once the buffer is flushed.
    foreach ($worker->connections as $connection) {
        $connection->close();
    }
};

Worker::runAll();
