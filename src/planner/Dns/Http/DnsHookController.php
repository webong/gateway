<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns\Http;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use RuntimeException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Dns\DnsHookRegistry;
use Webong\Gateway\Dns\DnsHookResources;
use Webong\Gateway\Dns\DnsZoneCatalog;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\WebProxy\Models\WebProxyEndpoint;

final readonly class DnsHookController
{
    public function __construct(
        private ValidationFactory $validator,
        private RegistryRequestAuthenticator $authenticator,
        private DnsHookRegistry $hooks,
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

        $hooks = WebProxyEndpoint::query()
            ->where('client', trim((string) config('gateway.dns.client', 'gateway')))
            ->where('metadata->_gateway->kind', 'dns_hook')
            ->where('metadata->_gateway->deleted', false)
            ->when($zone !== null, fn ($query) => $query->where('metadata->_gateway->zone', $zone))
            ->when($request->boolean('active'), fn ($query) => $query->where('is_active', true))
            ->latest()
            ->get()
            ->map(DnsHookResources::hook(...));

        return response()->json(['data' => $hooks]);
    }

    public function store(Request $request): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }
        if ($this->zones->all() === []) {
            return response()->json([
                'message' => 'DNS hooks are not configured. Set GATEWAY_DNS_ZONE or GATEWAY_DNS_ZONES.',
            ], Response::HTTP_SERVICE_UNAVAILABLE);
        }

        $input = $request->json()->all();
        if (array_diff(array_keys($input), ['external_id', 'name', 'metadata', 'registry', 'zone']) !== []) {
            return $this->invalid(['hook' => ['The request contains unsupported properties.']]);
        }
        $validator = $this->validator->make($input, [
            'external_id' => ['required', 'string', 'max:255'],
            'name' => ['nullable', 'string', 'max:255'],
            'metadata' => ['sometimes', 'array'],
            'registry' => ['nullable', 'string', 'max:255'],
            'zone' => ['nullable', 'string', 'max:253'],
        ]);
        if ($validator->fails()) {
            return $this->invalid($validator->errors()->toArray());
        }

        try {
            $hook = $this->hooks->ensure(
                externalId: $input['external_id'],
                name: $input['name'] ?? null,
                metadata: $input['metadata'] ?? [],
                registry: $input['registry'] ?? null,
                requestedZone: $input['zone'] ?? null,
            );
        } catch (InvalidArgumentException|RuntimeException $exception) {
            return $this->invalid(['hook' => [$exception->getMessage()]]);
        }

        return response()->json(DnsHookResources::hook($hook), Response::HTTP_CREATED);
    }

    public function show(Request $request, string $hook): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }
        $model = $this->hooks->find($hook);

        return $model === null
            ? response()->json(['message' => 'DNS hook not found.'], Response::HTTP_NOT_FOUND)
            : response()->json(DnsHookResources::hook($model));
    }

    public function update(Request $request, string $hook): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }
        $model = $this->hooks->find($hook);
        if ($model === null) {
            return response()->json(['message' => 'DNS hook not found.'], Response::HTTP_NOT_FOUND);
        }

        $input = $request->json()->all();
        if ($input === [] || array_diff(array_keys($input), ['name', 'active', 'metadata']) !== []) {
            return $this->invalid(['hook' => ['Provide one or more supported properties: name, active, metadata.']]);
        }
        $validator = $this->validator->make($input, [
            'name' => ['nullable', 'string', 'max:255'],
            'active' => ['sometimes', 'boolean'],
            'metadata' => ['sometimes', 'array'],
        ]);
        if ($validator->fails()) {
            return $this->invalid($validator->errors()->toArray());
        }

        $attributes = $validator->validated();
        if (array_key_exists('active', $attributes)) {
            $attributes['is_active'] = $attributes['active'];
            unset($attributes['active']);
        }

        return response()->json(DnsHookResources::hook($this->hooks->update($model, $attributes)));
    }

    public function destroy(Request $request, string $hook): Response
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }
        $model = $this->hooks->find($hook);
        if ($model === null) {
            return response()->json(['message' => 'DNS hook not found.'], Response::HTTP_NOT_FOUND);
        }

        $this->hooks->delete($model);

        return response()->noContent();
    }

    /** @param array<string, mixed> $errors */
    private function invalid(array $errors): JsonResponse
    {
        return response()->json([
            'message' => 'The DNS hook is invalid.',
            'errors' => $errors,
        ], Response::HTTP_UNPROCESSABLE_ENTITY);
    }
}
