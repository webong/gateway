<?php

declare(strict_types=1);

namespace Gateway\Router;

use Illuminate\Foundation\Application;
use Illuminate\Http\Request as LaravelRequest;
use Laravel\Octane\ApplicationFactory;
use Laravel\Octane\Contracts\Client;
use Laravel\Octane\Octane;
use Laravel\Octane\OctaneResponse;
use Laravel\Octane\RequestContext;
use Laravel\Octane\Worker as OctaneWorker;
use Symfony\Component\HttpFoundation\BinaryFileResponse;
use Symfony\Component\HttpFoundation\StreamedResponse;
use Throwable;
use Workerman\Protocols\Http\Request as WorkermanRequest;
use Workerman\Protocols\Http\Response as WorkermanResponse;

final class WorkermanLaravelHandler implements Client
{
    private OctaneWorker $worker;

    public function __construct(string $applicationBasePath)
    {
        $this->worker = new OctaneWorker(new ApplicationFactory($applicationBasePath), $this);
        $this->worker->boot();
    }

    public function handle(WorkermanRequest $request): WorkermanResponse
    {
        $context = new RequestContext(['workermanRequest' => $request, 'response' => null]);
        [$laravelRequest, $context] = $this->marshalRequest($context);
        try {
            $this->worker->handle($laravelRequest, $context);
        } finally {
            // A request may leave a transaction open (for example after an
            // uncaught exception). Roll it back so it cannot leak state or lock
            // rows into the next request served by this persistent worker.
            $this->rollbackUnfinishedTransactions();
        }

        return $context->response ?? new WorkermanResponse(500, ['Content-Type' => 'text/plain'], 'Internal Server Error');
    }

    /**
     * Roll back any database transaction left open by the request. The Octane
     * worker shares its database connections between the base application and
     * each request sandbox, so an unrolled-back transaction would otherwise
     * persist to the next request on the same worker.
     *
     * Each connection is handled on its own. A connection whose rollback fails is
     * discarded (so the next request opens a clean one) and cleanup carries on with
     * the remaining connections: one broken connection must not leave another
     * connection's transaction open.
     */
    private function rollbackUnfinishedTransactions(): void
    {
        try {
            $db = $this->worker->application()->make('db');
            $connections = $db->getConnections();
        } catch (Throwable) {
            // Transaction cleanup must never mask the response.
            return;
        }

        foreach ($connections as $name => $connection) {
            try {
                // Bounded by the depth we found, so a connection that keeps
                // reporting an open transaction cannot spin this loop forever.
                for ($open = $connection->transactionLevel(); $open > 0; $open--) {
                    $connection->rollBack();
                }
            } catch (Throwable) {
                $this->discardConnection($db, (string) $name, $connection);
            }
        }
    }

    private function discardConnection(object $db, string $name, object $connection): void
    {
        try {
            $db->purge($name);
        } catch (Throwable) {
            try {
                $connection->disconnect();
            } catch (Throwable) {
                // Nothing left to do for a connection that cannot even disconnect.
            }
        }
    }

    public function terminate(): void
    {
        $this->worker->terminate();
    }

    public function marshalRequest(RequestContext $context): array
    {
        return [$this->toLaravelRequest($context->workermanRequest), $context];
    }

    public function respond(RequestContext $context, OctaneResponse $octaneResponse): void
    {
        $response = $octaneResponse->response;
        $workermanResponse = new WorkermanResponse(
            status: $response->getStatusCode(),
            headers: $response->headers->all(),
        );

        if ($response instanceof BinaryFileResponse) {
            $context->response = $workermanResponse->withFile($response->getFile()->getPathname());

            return;
        }

        if ($response instanceof StreamedResponse) {
            ob_start();
            $response->sendContent();
            $content = ob_get_clean();
        } else {
            $content = $response->getContent();
        }

        $context->response = $workermanResponse->withBody(
            ($octaneResponse->outputBuffer ?? '').($content ?: ''),
        );
    }

    public function error(Throwable $exception, Application $app, LaravelRequest $request, RequestContext $context): void
    {
        $context->response = new WorkermanResponse(
            500,
            ['Content-Type' => 'text/plain; charset=UTF-8'],
            Octane::formatExceptionForClient($exception, $app->make('config')->get('app.debug')),
        );
    }

    private function toLaravelRequest(WorkermanRequest $request): LaravelRequest
    {
        $headers = $request->header();
        $server = [
            'REQUEST_METHOD' => $request->method(),
            'REQUEST_URI' => $request->uri(),
            'QUERY_STRING' => (string) (parse_url($request->uri(), PHP_URL_QUERY) ?? ''),
            'SERVER_PROTOCOL' => 'HTTP/'.$request->protocolVersion(),
            'HTTP_HOST' => $request->host() ?? 'localhost',
        ];

        foreach ($headers as $name => $value) {
            $key = strtoupper(str_replace('-', '_', (string) $name));
            $server[in_array($key, ['CONTENT_TYPE', 'CONTENT_LENGTH'], true) ? $key : 'HTTP_'.$key] = $value;
        }

        return LaravelRequest::create(
            uri: $request->uri(),
            method: $request->method(),
            parameters: $request->post(),
            cookies: $request->cookie(),
            files: [],
            server: $server,
            content: $request->rawBody(),
        );
    }
}
