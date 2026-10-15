<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns\Http;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use LogicException;
use RuntimeException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Dns\DnsProviderRegistry;
use Webong\Gateway\Dns\DnsZoneCatalog;
use Webong\Gateway\Dns\DnsZoneProvisioningFailed;
use Webong\Gateway\Dns\DnsZoneRegistry;
use Webong\Gateway\Dns\DnsZoneResources;
use Webong\Gateway\RegistryRequestAuthenticator;

final readonly class DnsZoneController
{
    public function __construct(
        private ValidationFactory $validator,
        private RegistryRequestAuthenticator $authenticator,
        private DnsZoneCatalog $catalog,
        private DnsZoneRegistry $zones,
        private DnsProviderRegistry $providers,
    ) {}

    public function index(Request $request): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }

        $resources = [];
        foreach ($this->catalog->all() as $zone) {
            $resources[$zone['name']] = $zone + ['provider' => 'manual', 'status' => 'active', 'active' => true];
        }
        foreach ($this->zones->all() as $zone) {
            $resource = DnsZoneResources::endpoint($zone);
            $resources[(string) $resource['name']] = $resource;
        }
        ksort($resources);

        return response()->json(['data' => array_values($resources)]);
    }

    public function store(Request $request): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }

        $input = $request->json()->all();
        if (array_diff(array_keys($input), ['name', 'nameservers', 'soa_email', 'ttl', 'provider', 'provider_options', 'registry']) !== []) {
            return $this->invalid(['zone' => ['The request contains unsupported properties.']]);
        }
        $validator = $this->validator->make($input, [
            'name' => ['required', 'string', 'max:253', 'regex:/^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.?$/i'],
            'nameservers' => ['sometimes', 'array', 'min:1'],
            'nameservers.*' => ['required', 'string', 'max:253', 'regex:/^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.?$/i'],
            'soa_email' => ['nullable', 'string', 'max:253', 'regex:/^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.?$/i'],
            'ttl' => ['sometimes', 'integer', 'min:60', 'max:86400'],
            'provider' => ['sometimes', 'string', 'max:64'],
            'provider_options' => ['sometimes', 'array:parent_zone_id,parent_zone'],
            'provider_options.parent_zone_id' => ['sometimes', 'string', 'max:255'],
            'provider_options.parent_zone' => ['sometimes', 'string', 'max:253'],
            'registry' => ['nullable', 'string', 'max:255'],
        ]);
        if ($validator->fails()) {
            return $this->invalid($validator->errors()->toArray());
        }
        $attributes = $validator->validated();
        $name = strtolower(rtrim($attributes['name'], '.'));
        if ($this->zones->find($name) !== null) {
            return response()->json(['message' => 'DNS zone already exists.'], Response::HTTP_CONFLICT);
        }
        $provider = strtolower((string) ($attributes['provider'] ?? 'manual'));
        if (! in_array($provider, $this->providers->names(), true)) {
            return $this->invalid(['provider' => ['The selected DNS provider is not registered.']]);
        }
        $nameservers = $attributes['nameservers'] ?? config('gateway.dns.default_nameservers', []);
        if (! is_array($nameservers) || $nameservers === []) {
            return $this->invalid(['nameservers' => ['Provide nameservers or configure gateway.dns.default_nameservers.']]);
        }

        try {
            $zone = $this->zones->create(
                name: $name,
                nameservers: array_map(static fn (string $server): string => strtolower(rtrim(trim($server), '.')), $nameservers),
                provider: $provider,
                providerOptions: $attributes['provider_options'] ?? [],
                soaEmail: $attributes['soa_email'] ?? null,
                ttl: (int) ($attributes['ttl'] ?? 60),
                registry: $attributes['registry'] ?? null,
            );
        } catch (DnsZoneProvisioningFailed $exception) {
            return response()->json([
                'message' => $exception->getMessage(),
                'data' => DnsZoneResources::endpoint($exception->zone),
            ], Response::HTTP_BAD_GATEWAY);
        } catch (InvalidArgumentException|RuntimeException $exception) {
            return $this->invalid(['zone' => [$exception->getMessage()]]);
        }

        return response()->json(DnsZoneResources::endpoint($zone), Response::HTTP_CREATED);
    }

    public function show(Request $request, string $zone): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }
        $model = $this->zones->find($zone);

        return $model === null
            ? response()->json(['message' => 'DNS zone not found.'], Response::HTTP_NOT_FOUND)
            : response()->json(DnsZoneResources::endpoint($model));
    }

    public function provision(Request $request, string $zone): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }
        $model = $this->zones->find($zone);
        if ($model === null) {
            return response()->json(['message' => 'DNS zone not found.'], Response::HTTP_NOT_FOUND);
        }
        try {
            return response()->json(DnsZoneResources::endpoint($this->zones->provision($model)));
        } catch (DnsZoneProvisioningFailed $exception) {
            return response()->json([
                'message' => $exception->getMessage(),
                'data' => DnsZoneResources::endpoint($exception->zone),
            ], Response::HTTP_BAD_GATEWAY);
        }
    }

    public function destroy(Request $request, string $zone): Response
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }
        $model = $this->zones->find($zone);
        if ($model === null) {
            return response()->json(['message' => 'DNS zone not found.'], Response::HTTP_NOT_FOUND);
        }
        try {
            $this->zones->delete($model);
        } catch (LogicException $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_CONFLICT);
        } catch (DnsZoneProvisioningFailed $exception) {
            return response()->json([
                'message' => $exception->getMessage(),
                'data' => DnsZoneResources::endpoint($exception->zone),
            ], Response::HTTP_BAD_GATEWAY);
        }

        return response()->noContent();
    }

    /** @param array<string, mixed> $errors */
    private function invalid(array $errors): JsonResponse
    {
        return response()->json(['message' => 'The given data was invalid.', 'errors' => $errors], Response::HTTP_UNPROCESSABLE_ENTITY);
    }
}
