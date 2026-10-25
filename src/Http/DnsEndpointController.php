<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns\Http;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use RuntimeException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Dns\DnsEndpointRegistry;
use Webong\Gateway\Dns\DnsEndpointResources;
use Webong\Gateway\Dns\DnsZoneCatalog;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\Gateway\Resources\ResourcePrecondition;
use Webong\Gateway\Resources\StaleResource;
use Webong\Proxy\Models\WebProxyEndpoint;

final readonly class DnsEndpointController
{
    public function __construct(
        private ValidationFactory $validator,
        private RegistryRequestAuthenticator $authenticator,
        private DnsEndpointRegistry $endpoints,
        private DnsZoneCatalog $zones,
    ) {}

    public function index(Request $request): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }

        $zone = $request->query('zone');
        if ($zone !== null && ! is_string($zone)) {
            return $this->invalid(['zone' => ['The zone must be a string.']]);
        }
        try {
            $zone = $zone !== null ? $this->zones->resolve($zone) : null;
        } catch (InvalidArgumentException $exception) {
            return $this->invalid(['zone' => [$exception->getMessage()]]);
        }

        $endpoints = WebProxyEndpoint::query()
            ->where('client', DnsEndpointRegistry::CLIENT)
            ->where('metadata->_gateway->kind', DnsEndpointRegistry::KIND)
            ->where('metadata->_gateway->deleted', false)
            ->when($zone !== null, fn ($query) => $query->where('metadata->_gateway->zone', $zone))
            ->when($request->boolean('enabled'), fn ($query) => $query->where('is_active', true))
            ->latest()
            ->get()
            ->map(DnsEndpointResources::endpoint(...));

        return response()->json(['data' => $endpoints]);
    }

    public function store(Request $request): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }
        if ($this->zones->all() === []) {
            return response()->json([
                'message' => 'No active DNS zones are configured. Create one through POST /dns/zones.',
            ], Response::HTTP_SERVICE_UNAVAILABLE);
        }

        $input = $request->json()->all();
        if (array_diff(array_keys($input), ['external_id', 'name', 'metadata', 'registry', 'zone']) !== []) {
            return $this->invalid(['endpoint' => ['The request contains unsupported properties.']]);
        }
        $validator = $this->validator->make($input, [
            'external_id' => ['required', 'string', 'min:1', 'max:255'],
            'name' => ['nullable', 'string', 'max:255'],
            'metadata' => ['sometimes', 'array'],
            'registry' => ['nullable', 'string', 'max:255'],
            'zone' => ['nullable', 'string', 'max:253'],
        ]);
        if ($validator->fails()) {
            return $this->invalid($validator->errors()->toArray());
        }

        try {
            $endpoint = $this->endpoints->ensure(
                externalId: $input['external_id'],
                name: $input['name'] ?? null,
                metadata: $input['metadata'] ?? [],
                registry: $input['registry'] ?? null,
                requestedZone: $input['zone'] ?? null,
            );
        } catch (InvalidArgumentException|RuntimeException $exception) {
            return $this->invalid(['endpoint' => [$exception->getMessage()]]);
        }

        $resource = DnsEndpointResources::endpoint($endpoint);

        return response()->json($resource, Response::HTTP_CREATED)
            ->header('ETag', ResourcePrecondition::etag($resource['revision']));
    }

    public function show(Request $request, string $endpoint): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }
        $model = $this->endpoints->find($endpoint);

        return $model === null
            ? response()->json(['message' => 'DNS endpoint not found.'], Response::HTTP_NOT_FOUND)
            : response()->json(DnsEndpointResources::endpoint($model))
                ->header('ETag', ResourcePrecondition::etag((int) ($this->endpoints->gateway($model)['revision'] ?? 1)));
    }

    public function update(Request $request, string $endpoint): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }
        $model = $this->endpoints->find($endpoint);
        if ($model === null) {
            return response()->json(['message' => 'DNS endpoint not found.'], Response::HTTP_NOT_FOUND);
        }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) {
            return response()->json(['message' => 'A current If-Match revision is required.'], Response::HTTP_PRECONDITION_REQUIRED);
        }

        $input = $request->json()->all();
        if ($input === [] || array_diff(array_keys($input), ['name', 'enabled', 'metadata']) !== []) {
            return $this->invalid(['endpoint' => ['Provide one or more supported properties: name, enabled, metadata.']]);
        }
        $validator = $this->validator->make($input, [
            'name' => ['nullable', 'string', 'max:255'],
            'enabled' => ['sometimes', 'boolean'],
            'metadata' => ['sometimes', 'array'],
        ]);
        if ($validator->fails()) {
            return $this->invalid($validator->errors()->toArray());
        }

        $attributes = $validator->validated();
        if (array_key_exists('enabled', $attributes)) {
            $attributes['is_active'] = $attributes['enabled'];
            unset($attributes['enabled']);
        }

        try {
            $updated = $this->endpoints->update($model, $attributes, $revision);
        } catch (StaleResource) {
            return response()->json(['message' => 'DNS endpoint has changed.'], Response::HTTP_PRECONDITION_FAILED);
        }
        $resource = DnsEndpointResources::endpoint($updated);

        return response()->json($resource)->header('ETag', ResourcePrecondition::etag($resource['revision']));
    }

    public function destroy(Request $request, string $endpoint): Response
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }
        $model = $this->endpoints->find($endpoint);
        if ($model === null) {
            return response()->json(['message' => 'DNS endpoint not found.'], Response::HTTP_NOT_FOUND);
        }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) {
            return response()->json(['message' => 'A current If-Match revision is required.'], Response::HTTP_PRECONDITION_REQUIRED);
        }

        try {
            $this->endpoints->delete($model, $revision);
        } catch (StaleResource) {
            return response()->json(['message' => 'DNS endpoint has changed.'], Response::HTTP_PRECONDITION_FAILED);
        }

        return response()->noContent();
    }

    /** @param array<string, mixed> $errors */
    private function invalid(array $errors): JsonResponse
    {
        return response()->json([
            'message' => 'The DNS endpoint is invalid.',
            'errors' => $errors,
        ], Response::HTTP_UNPROCESSABLE_ENTITY);
    }
}
