<?php

declare(strict_types=1);

namespace Webong\Gateway\Servers\Http\Controllers;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\Gateway\Servers\Models\Instance;
use Webong\Gateway\Servers\Resources\ServerResources;

final readonly class InstanceController
{
    public function __construct(private RegistryRequestAuthenticator $authenticator) {}

    public function index(Request $request): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }

        $instances = Instance::query()
            ->when(
                is_string($request->query('server_id')) && $request->query('server_id') !== '',
                fn ($query) => $query->where('server_id', $request->query('server_id')),
            )
            ->latest('heartbeat_at')
            ->get()
            ->map(ServerResources::instance(...));

        return response()->json(['data' => $instances]);
    }

    public function show(Request $request, string $instance): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }
        $model = Instance::query()->find($instance);

        return $model === null
            ? response()->json(['message' => 'Instance not found.'], Response::HTTP_NOT_FOUND)
            : response()->json(ServerResources::instance($model));
    }
}
