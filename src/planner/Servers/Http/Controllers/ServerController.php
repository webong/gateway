<?php

declare(strict_types=1);

namespace Webong\Gateway\Servers\Http\Controllers;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Contracts\Validation\Validator;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Illuminate\Validation\Rule;
use Illuminate\Validation\ValidationException;
use LogicException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\Gateway\Servers\Models\Server;
use Webong\Gateway\Servers\ServerRegistry;
use Webong\Gateway\Servers\ServerTables;
use Webong\Gateway\Servers\ServerTypeRegistry;

final readonly class ServerController
{
    use ControllerConcerns;

    public function __construct(
        private ValidationFactory $validator,
        private RegistryRequestAuthenticator $authenticator,
        private ServerTypeRegistry $types,
        private ServerRegistry $registry,
    ) {}

    public function index(Request $request): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }

        $servers = Server::query()
            ->withCount(['applications', 'instances' => fn ($query) => $query->whereIn('state', ['starting', 'running'])])
            ->orderBy('name')
            ->get()
            ->map(function (Server $server): ?array {
                $type = $this->types->find((string) $server->type);

                return $type === null ? null : $this->serverResource($server, $type);
            })
            ->filter()
            ->values();

        return response()->json(['data' => $servers]);
    }

    public function show(Request $request, string $server): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }

        $model = Server::query()
            ->withCount(['applications', 'instances' => fn ($query) => $query->whereIn('state', ['starting', 'running'])])
            ->find($server);
        if ($model === null) {
            return response()->json(['message' => 'Server not found.'], Response::HTTP_NOT_FOUND);
        }
        $type = $this->serverType($model, $this->types);
        if ($type === null) {
            return response()->json(['message' => "Server type [{$model->type}] is not available."], Response::HTTP_CONFLICT);
        }

        return response()->json($this->serverResource($model, $type));
    }

    public function store(Request $request): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }

        $input = $request->json()->all();
        if (($unsupported = array_values(array_diff(array_keys($input), ['type', 'name', 'hostname', 'path', 'driver', 'replicas', 'configuration']))) !== []) {
            return $this->invalid('The server is invalid.', ['server' => ['Unsupported properties: '.implode(', ', $unsupported).'.']]);
        }

        $typeName = is_string($input['type'] ?? null) ? strtolower($input['type']) : '';
        $type = $this->types->find($typeName);
        if ($type === null) {
            return $this->invalid('The server is invalid.', ['type' => ['The selected server type is invalid. Registered types: '.implode(', ', $this->types->names()).'.']]);
        }

        if (isset($input['hostname']) && is_string($input['hostname'])) {
            $input['hostname'] = strtolower($input['hostname']);
        }
        $input['path'] = ServerRegistry::normalizePath((string) ($input['path'] ?? $type->defaultPath()));
        $validator = $this->validator->make($input, $this->rules());
        $this->ensureRouteIsUnique($validator, $input['hostname'] ?? null, $input['path']);
        if ($validator->fails()) {
            return $this->invalid('The server is invalid.', $validator->errors()->toArray());
        }

        $validated = $validator->validated();
        try {
            $validated['configuration'] = $type->validateConfiguration(
                is_array($validated['configuration'] ?? null) ? $validated['configuration'] : [],
                null,
                (int) ($validated['replicas'] ?? 1),
            );
        } catch (ValidationException $exception) {
            return $this->validationFailure($type, $exception);
        }

        return response()->json(
            $this->serverResource($this->registry->createServer($type, $validated), $type),
            Response::HTTP_CREATED,
        );
    }

    public function update(Request $request, string $server): JsonResponse
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

        $input = $request->json()->all();
        if ($input === []) {
            return $this->invalid('The server is invalid.', ['server' => ['At least one property is required.']]);
        }
        if (($unsupported = array_values(array_diff(array_keys($input), ['name', 'hostname', 'path', 'driver', 'replicas', 'configuration']))) !== []) {
            return $this->invalid('The server is invalid.', ['server' => ['Unsupported properties: '.implode(', ', $unsupported).'.']]);
        }
        if (isset($input['hostname']) && is_string($input['hostname'])) {
            $input['hostname'] = strtolower($input['hostname']);
        }
        if (array_key_exists('path', $input)) {
            $input['path'] = ServerRegistry::normalizePath((string) $input['path']);
        }

        $validator = $this->validator->make($input, $this->rules($model));
        $this->ensureRouteIsUnique(
            $validator,
            $input['hostname'] ?? $model->hostname,
            $input['path'] ?? $model->path,
            $model,
        );
        if ($validator->fails()) {
            return $this->invalid('The server is invalid.', $validator->errors()->toArray());
        }

        $validated = $validator->validated();
        if (array_key_exists('configuration', $validated) || array_key_exists('replicas', $validated)) {
            try {
                $configuration = $type->validateConfiguration(
                    is_array($validated['configuration'] ?? null) ? $validated['configuration'] : [],
                    $model,
                    (int) ($validated['replicas'] ?? $model->replicas),
                );
                if (array_key_exists('configuration', $validated)) {
                    $validated['configuration'] = $configuration;
                }
            } catch (ValidationException $exception) {
                return $this->validationFailure($type, $exception);
            }
        }

        return response()->json($this->serverResource($this->registry->updateServer($model, $type, $validated), $type));
    }

    public function destroy(Request $request, string $server): Response
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }
        $model = Server::query()->find($server);
        if ($model === null) {
            return response()->json(['message' => 'Server not found.'], Response::HTTP_NOT_FOUND);
        }
        try {
            $this->registry->deleteServer($model);
        } catch (LogicException $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_CONFLICT);
        }

        return response()->noContent();
    }

    /** @return array<string, mixed> */
    private function rules(?Server $server = null): array
    {
        $required = $server === null ? 'required' : 'sometimes';

        return [
            'type' => [$server === null ? 'required' : 'prohibited', 'string'],
            'name' => [$required, 'string', 'max:255', Rule::unique(ServerTables::servers(), 'name')->ignore($server?->getKey())],
            'hostname' => [$required, 'string', 'max:253', 'regex:/^(?=.{1,253}$)(?!-)[A-Za-z0-9.-]+(?<!-)$/'],
            'path' => ['sometimes', 'nullable', 'string', 'max:255', 'regex:/^(\/[^?#]*)?$/'],
            'driver' => ['sometimes', Rule::in(['local', 'docker', 'kubernetes'])],
            'replicas' => ['sometimes', 'integer', 'min:1', 'max:64'],
            'configuration' => ['sometimes', 'array'],
        ];
    }

    private function ensureRouteIsUnique(Validator $validator, mixed $hostname, string $path, ?Server $server = null): void
    {
        $validator->after(function (Validator $validator) use ($hostname, $path, $server): void {
            if (! is_string($hostname) || $hostname === '') {
                return;
            }
            $exists = Server::query()->where('hostname', strtolower($hostname))->where('path', $path)
                ->when($server !== null, fn ($query) => $query->where('id', '!=', $server->getKey()))->exists();
            if ($exists) {
                $validator->errors()->add('hostname', 'The hostname and path are already assigned to another server.');
            }
        });
    }
}
