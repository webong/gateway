<?php

declare(strict_types=1);

namespace Webong\Gateway\Servers\Http\Controllers;

use Illuminate\Contracts\Validation\Factory;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Illuminate\Support\Str;
use Illuminate\Validation\Rule;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\Gateway\Servers\Models\DeploymentTarget;
use Webong\Gateway\Servers\Models\ProvisioningNode;
use Webong\Gateway\Servers\Models\Server;
use Webong\Gateway\Servers\ServerTables;

final readonly class TargetController
{
    public function __construct(private Factory $validator, private RegistryRequestAuthenticator $authenticator) {}

    public function index(Request $request): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }

        return response()->json(['data' => DeploymentTarget::query()->orderBy('name')->get()->map(self::resource(...))]);
    }

    public function show(Request $request, string $target): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }

        return response()->json(self::resource(DeploymentTarget::query()->findOrFail($target)));
    }

    public function store(Request $request): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $input = $request->json()->all();
        $driver = is_string($input['driver'] ?? null) ? $input['driver'] : '';
        $connectionRules = match ($driver) {
            'docker' => [
                'connection' => ['required', 'array:context,publish_host,route_host,network'],
                'connection.context' => ['required', 'string', 'max:255'],
                'connection.publish_host' => ['required', 'ip'],
                'connection.route_host' => ['required', 'ip', 'not_in:0.0.0.0,::'],
                'connection.network' => ['sometimes', 'string', 'max:255'],
            ],
            'kubernetes' => [
                'connection' => ['required', 'array:context,kubeconfig,namespace,route_mode'],
                'connection.context' => ['required', 'string', 'max:255'],
                'connection.kubeconfig' => ['required', 'string', 'max:4096', 'starts_with:/'],
                'connection.namespace' => ['required', 'string', 'max:63', 'regex:/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/'],
                'connection.route_mode' => ['required', 'in:pod-ip,port-forward'],
            ],
            default => ['connection' => ['sometimes', 'array', 'size:0']],
        };
        $validated = $this->validator->make(['target' => $input], [
            'target' => ['required', 'array:name,driver,connection'],
        ])->validate()['target'];
        $validated = $this->validator->make($validated, [
            'name' => ['required', 'string', 'max:255', Rule::unique(ServerTables::targets(), 'name')],
            'driver' => ['required', 'in:local,docker,kubernetes'],
            ...$connectionRules,
        ])->validate();
        $token = Str::random(64);
        $target = DeploymentTarget::query()->create([
            ...$validated,
            'connection' => $validated['connection'] ?? [],
            'worker_token_hash' => hash('sha256', $token),
        ]);

        return response()->json([...self::resource($target), 'worker_token' => $token], Response::HTTP_CREATED);
    }

    public function rotateToken(Request $request, string $target): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $model = DeploymentTarget::query()->findOrFail($target);
        $token = Str::random(64);
        $model->update(['worker_token_hash' => hash('sha256', $token)]);

        return response()->json(['worker_token' => $token]);
    }

    public function destroy(Request $request, string $target): Response
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $model = DeploymentTarget::query()->findOrFail($target);
        if (Server::query()->where('target_id', $target)->exists() || ProvisioningNode::query()->where('target_id', $target)
            ->where('heartbeat_at', '>=', now()->subSeconds(max(2, (int) config('gateway.servers.coordination_lease_seconds', 15))))->exists()) {
            return response()->json(['message' => 'Remove attached servers and stop target workers before deleting the target.'], Response::HTTP_CONFLICT);
        }
        $model->getConnection()->transaction(function () use ($model, $target): void {
            ProvisioningNode::query()->where('target_id', $target)->delete();
            $model->delete();
        });

        return response()->noContent();
    }

    public static function resource(DeploymentTarget $target): array
    {
        $heartbeat = ProvisioningNode::query()->where('target_id', $target->getKey())->max('heartbeat_at');

        return [
            'id' => $target->getKey(),
            'name' => $target->name,
            'driver' => $target->driver,
            // Connection contains references only, never private keys or kubeconfig contents.
            'connection' => $target->connection ?: (object) [],
            'last_heartbeat_at' => $heartbeat,
        ];
    }
}
