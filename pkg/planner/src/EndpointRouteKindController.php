<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Planning\RoutePlannerRegistry;
use Webong\Proxy\WebProxyRegistryManager;
use Webong\Proxy\WebProxyChannelManager;

/** Operator CMS surface for inspecting and changing an endpoint's route kind. */
final readonly class EndpointRouteKindController
{
    public function __construct(
        private RegistryRequestAuthenticator $authenticator,
        private WebProxyRegistryManager $registries,
        private WebProxyChannelManager $channels,
        private RoutePlannerRegistry $routes,
        private RegistryRouteCache $cache,
    ) {}

    public function show(Request $request, string $endpointKey): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) { return $denied; }
        $resolved = $this->registries->resolveByKey($endpointKey, $this->channels->registries(null));
        if ($resolved === null) { return $this->missing(); }

        return response()->json([
            'endpoint_key' => $resolved->endpoint->record->endpoint_key,
            'route_kind' => EndpointRouteKind::fromMetadata($resolved->endpoint->record->metadata),
        ]);
    }

    public function update(Request $request, string $endpointKey): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) { return $denied; }
        $input = $request->json()->all();
        if (array_keys($input) !== ['route_kind'] || ! is_string($input['route_kind'])
            || preg_match('/\A[a-z][a-z0-9.-]{0,63}\z/D', $input['route_kind']) !== 1
            || ($input['route_kind'] !== 'http' && ! $this->routes->contains($input['route_kind']))) {
            return response()->json(['message' => 'The endpoint route kind is invalid.'], Response::HTTP_UNPROCESSABLE_ENTITY);
        }
        $resolved = $this->registries->resolveByKey($endpointKey, $this->channels->registries(null));
        if ($resolved === null) { return $this->missing(); }
        try {
            $metadata = EndpointRouteKind::withKind($resolved->endpoint->record->metadata, $input['route_kind']);
            $resolved->registrar->run(fn (): mixed => $resolved->endpoint->update(['metadata' => $metadata]));
        } catch (InvalidArgumentException $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_UNPROCESSABLE_ENTITY);
        }
        $this->cache->invalidatePaths();
        $this->cache->invalidateEndpoint($endpointKey);
        return response()->json(['endpoint_key' => $endpointKey, 'route_kind' => $input['route_kind']]);
    }

    private function missing(): JsonResponse
    {
        return response()->json(['message' => 'The endpoint was not found.'], Response::HTTP_NOT_FOUND);
    }
}
