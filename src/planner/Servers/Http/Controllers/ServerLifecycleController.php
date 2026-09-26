<?php

declare(strict_types=1);

namespace Webong\Gateway\Servers\Http\Controllers;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Illuminate\Validation\ValidationException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\Gateway\Servers\Models\Server;
use Webong\Gateway\Servers\Resources\ServerResources;
use Webong\Gateway\Servers\ServerRegistry;
use Webong\Gateway\Servers\ServerTypeRegistry;

final readonly class ServerLifecycleController
{
    use ControllerConcerns;

    public function __construct(
        private RegistryRequestAuthenticator $authenticator,
        private ValidationFactory $validator,
        private ServerTypeRegistry $types,
        private ServerRegistry $registry,
    ) {}

    public function start(Request $request, string $server): JsonResponse
    {
        return $this->transition($request, $server, 'running');
    }

    public function stop(Request $request, string $server): JsonResponse
    {
        return $this->transition($request, $server, 'stopped');
    }

    public function restart(Request $request, string $server): JsonResponse
    {
        return $this->transition($request, $server, 'running', true);
    }

    public function scale(Request $request, string $server): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }
        $model = Server::query()->find($server);
        if ($model === null) {
            return response()->json(['message' => 'Server not found.'], Response::HTTP_NOT_FOUND);
        }
        $type = $this->serverType($model, $this->types);
        if ($type === null) {
            return response()->json(['message' => "Server type [{$model->type}] is not available."], Response::HTTP_CONFLICT);
        }
        $validator = $this->validator->make($request->json()->all(), [
            'replicas' => ['required', 'integer', 'min:1', 'max:64'],
        ]);
        if ($validator->fails()) {
            return $this->invalid('The server scale request is invalid.', $validator->errors()->toArray());
        }
        $replicas = (int) $validator->validated()['replicas'];
        try {
            $type->validateConfiguration([], $model, $replicas);
        } catch (ValidationException $exception) {
            return $this->validationFailure($type, $exception);
        }

        return response()->json(
            $this->serverResource($this->registry->updateServer($model, $type, ['replicas' => $replicas]), $type),
            Response::HTTP_ACCEPTED,
        );
    }

    public function health(Request $request, string $server): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }
        $model = Server::query()->find($server);
        if ($model === null) {
            return response()->json(['message' => 'Server not found.'], Response::HTTP_NOT_FOUND);
        }

        return response()->json(ServerResources::health($model));
    }

    private function transition(Request $request, string $server, string $state, bool $replace = false): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }
        $model = Server::query()->find($server);
        if ($model === null) {
            return response()->json(['message' => 'Server not found.'], Response::HTTP_NOT_FOUND);
        }
        $type = $this->serverType($model, $this->types);
        if ($type === null) {
            return response()->json(['message' => "Server type [{$model->type}] is not available."], Response::HTTP_CONFLICT);
        }

        return response()->json(
            $this->serverResource($this->registry->setDesiredState($model, $state, $replace), $type),
            Response::HTTP_ACCEPTED,
        );
    }
}
