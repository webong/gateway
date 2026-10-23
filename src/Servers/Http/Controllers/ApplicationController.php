<?php

declare(strict_types=1);

namespace Webong\Gateway\Servers\Http\Controllers;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Illuminate\Validation\ValidationException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\Gateway\Servers\Contracts\ApplicationServerType;
use Webong\Gateway\Servers\Models\Application;
use Webong\Gateway\Servers\Models\Server;
use Webong\Gateway\Servers\ServerRegistry;
use Webong\Gateway\Servers\ServerTypeRegistry;

final readonly class ApplicationController
{
    use ControllerConcerns;

    public function __construct(
        private RegistryRequestAuthenticator $authenticator,
        private ServerTypeRegistry $types,
        private ServerRegistry $registry,
    ) {}

    public function index(Request $request): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }

        $applications = Application::query()
            ->with('server')
            ->when(
                is_string($request->query('server_id')) && $request->query('server_id') !== '',
                fn ($query) => $query->where('server_id', $request->query('server_id')),
            )
            ->orderBy('external_id')
            ->get()
            ->map(function (Application $application): ?array {
                $type = $this->applicationType($application->server);

                return $type?->applicationResource($application);
            })
            ->filter()
            ->values();

        return response()->json(['data' => $applications]);
    }

    public function store(Request $request): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }

        $input = $request->json()->all();
        $serverId = $input['server_id'] ?? null;
        if (! is_string($serverId) || $serverId === '') {
            return $this->invalid('The application is invalid.', ['server_id' => ['The server id field is required.']]);
        }
        unset($input['server_id']);

        $resolved = $this->resolveServer($serverId);
        if ($resolved instanceof JsonResponse) {
            return $resolved;
        }
        [$server, $type] = $resolved;
        try {
            $validated = $type->validateApplication($input, $server, null);
        } catch (ValidationException $exception) {
            return $this->validationFailure($type, $exception, 'application');
        }

        return response()->json(
            $type->applicationResource($this->registry->createApplication($server, $type, $validated)),
            Response::HTTP_CREATED,
        );
    }

    public function show(Request $request, string $application): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }
        $model = Application::query()->with('server')->find($application);
        if ($model === null) {
            return response()->json(['message' => 'Application not found.'], Response::HTTP_NOT_FOUND);
        }
        $type = $this->applicationType($model->server);
        if ($type === null) {
            return $this->unsupported($model->server);
        }

        return response()->json($type->applicationResource($model));
    }

    public function update(Request $request, string $application): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }
        $model = Application::query()->with('server')->find($application);
        if ($model === null) {
            return response()->json(['message' => 'Application not found.'], Response::HTTP_NOT_FOUND);
        }
        $type = $this->applicationType($model->server);
        if ($type === null) {
            return $this->unsupported($model->server);
        }
        $input = $request->json()->all();
        if ($input === []) {
            return $this->invalid('The application is invalid.', ['application' => ['At least one property is required.']]);
        }
        if (array_key_exists('server_id', $input)) {
            return $this->invalid('The application is invalid.', ['server_id' => ['An application cannot be moved to another server.']]);
        }
        try {
            $validated = $type->validateApplication($input, $model->server, $model);
        } catch (ValidationException $exception) {
            return $this->validationFailure($type, $exception, 'application');
        }

        return response()->json($type->applicationResource(
            $this->registry->updateApplication($model, $type, $validated),
        ));
    }

    public function destroy(Request $request, string $application): Response
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }
        $model = Application::query()->find($application);
        if ($model === null) {
            return response()->json(['message' => 'Application not found.'], Response::HTTP_NOT_FOUND);
        }
        $model->delete();

        return response()->noContent();
    }

    /** @return array{Server, ApplicationServerType}|JsonResponse */
    private function resolveServer(string $server): array|JsonResponse
    {
        $model = Server::query()->find($server);
        if ($model === null) {
            return response()->json(['message' => 'Server not found.'], Response::HTTP_NOT_FOUND);
        }
        $type = $this->applicationType($model);
        if ($type === null) {
            return $this->unsupported($model);
        }

        return [$model, $type];
    }

    private function applicationType(Server $server): ?ApplicationServerType
    {
        $type = $this->types->find((string) $server->type);

        return $type instanceof ApplicationServerType && $type->supportsApplications() ? $type : null;
    }

    private function unsupported(Server $server): JsonResponse
    {
        return response()->json([
            'message' => "Server type [{$server->type}] does not support applications.",
        ], Response::HTTP_UNPROCESSABLE_ENTITY);
    }
}
