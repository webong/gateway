<?php

declare(strict_types=1);

namespace Webong\Gateway\Reconciliation\Http\Controllers;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Reconciliation\InternalRequestAuthenticator;
use Webong\Gateway\Servers\Models\Server;
use Webong\Gateway\Servers\ServerTables;

final readonly class InternalRouteController
{
    public function __construct(private InternalRequestAuthenticator $authenticator) {}

    public function __invoke(Request $request): JsonResponse
    {
        // A target worker must not inspect routes belonging to other targets.
        abort_if($request->hasHeader('X-Gateway-Target'), Response::HTTP_NOT_FOUND);
        $this->authenticator->authorizeOrHide($request);

        $hostname = $request->query('hostname');
        $path = $request->query('path');
        if (! is_string($hostname) || $hostname === '' || strlen($hostname) > 255 ||
            ! is_string($path) || $path === '' || $path[0] !== '/' || strlen($path) > 4096) {
            return response()->json(['message' => 'Invalid route lookup.'], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        $servers = Server::query()
            ->whereRaw('LOWER(hostname) = ?', [strtolower($hostname)])
            ->get(['id', 'path', 'desired_state', 'revision', 'target_id'])
            ->sortByDesc(static fn (Server $server): int => strlen((string) $server->path));

        foreach ($servers as $server) {
            $prefix = (string) $server->path;
            if ($prefix !== '' && $prefix !== '/' && $path !== $prefix && ! str_starts_with($path, rtrim($prefix, '/').'/')) {
                continue;
            }

            if ($server->desired_state !== 'running') {
                return response()->json(['message' => 'Managed server is stopped.'], Response::HTTP_SERVICE_UNAVAILABLE);
            }

            $lease = max(2, (int) config('gateway.servers.coordination_lease_seconds', 15));
            $cutoff = now()->subSeconds($lease);
            $instance = $server->instances()
                ->where('state', 'running')
                ->where('revision', $server->revision)
                ->where('heartbeat_at', '>=', $cutoff)
                ->whereNotNull('host')
                ->whereNotNull('port')
                ->whereIn('node_id', fn ($query) => $query->select('id')
                    ->from(ServerTables::nodes())
                    ->where('target_id', $server->target_id)
                    ->where('heartbeat_at', '>=', $cutoff))
                ->inRandomOrder()
                ->first();

            if ($instance === null) {
                return response()->json(['message' => 'Managed server has no healthy instance.'], Response::HTTP_SERVICE_UNAVAILABLE);
            }

            return response()->json(['data' => [
                'server_id' => $server->getKey(),
                'instance_id' => $instance->getKey(),
                'host' => $instance->host,
                'port' => $instance->port,
            ]]);
        }

        return response()->json(null, Response::HTTP_NO_CONTENT);
    }
}
