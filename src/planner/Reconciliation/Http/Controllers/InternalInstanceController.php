<?php

declare(strict_types=1);

namespace Webong\Gateway\Reconciliation\Http\Controllers;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Reconciliation\InternalRequestAuthenticator;
use Webong\Gateway\Servers\Models\Instance;
use Webong\Gateway\Servers\Models\Server;
use Webong\Gateway\Servers\Resources\ServerResources;

final readonly class InternalInstanceController
{
    public function __construct(
        private ValidationFactory $validator,
        private InternalRequestAuthenticator $authenticator,
    ) {
    }

    public function update(Request $request, string $server, string $instance): JsonResponse
    {
        $this->authenticator->authorizeOrHide($request);

        if (! Server::query()->whereKey($server)->exists()) {
            return response()->json(['message' => 'Provisioned server not found.'], Response::HTTP_NOT_FOUND);
        }

        $validator = $this->validator->make($request->json()->all(), [
            'node_id' => ['required', 'string', 'max:255'],
            'runtime' => ['required', 'in:local,docker,kubernetes'],
            'pid' => ['nullable', 'integer', 'min:1'],
            'host' => ['nullable', 'required_if:state,starting,running,stopping', 'ip'],
            'port' => ['nullable', 'required_if:state,starting,running,stopping', 'integer', 'min:1', 'max:65535'],
            'state' => ['required', 'in:starting,running,stopping,stopped,failed'],
            'revision' => ['required', 'integer', 'min:1'],
            'error' => ['nullable', 'string', 'max:65535'],
        ]);
        if ($validator->fails()) {
            return response()->json([
                'message' => 'The provisioned instance report is invalid.',
                'errors' => $validator->errors()->toArray(),
            ], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        $attributes = $validator->validated();
        $now = now();
        $attributes['heartbeat_at'] = $now;
        if (in_array($attributes['state'], ['starting', 'running'], true)) {
            $attributes['started_at'] = Instance::query()->find($instance)?->started_at ?? $now;
            $attributes['stopped_at'] = null;
        } elseif (in_array($attributes['state'], ['stopped', 'failed'], true)) {
            $attributes['stopped_at'] = $now;
        }

        $model = Instance::query()->updateOrCreate(
            ['id' => $instance],
            ['server_id' => $server, ...$attributes],
        );

        return response()->json(ServerResources::instance($model));
    }

    public function resetNode(Request $request): JsonResponse
    {
        $this->authenticator->authorizeOrHide($request);

        $validator = $this->validator->make($request->json()->all(), [
            'node_id' => ['required', 'string', 'max:255'],
        ]);
        if ($validator->fails()) {
            return response()->json([
                'message' => 'The provisioning node reset is invalid.',
                'errors' => $validator->errors()->toArray(),
            ], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        $now = now();
        $updated = Instance::query()
            ->where('node_id', $validator->validated()['node_id'])
            ->whereIn('state', ['starting', 'running', 'stopping'])
            ->update([
                'state' => 'stopped',
                'error' => 'Gateway reconciler restarted.',
                'heartbeat_at' => $now,
                'stopped_at' => $now,
                'updated_at' => $now,
            ]);

        return response()->json(['updated' => $updated]);
    }
}
