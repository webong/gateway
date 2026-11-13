<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel\Http;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use RuntimeException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\EndpointRouteKind;
use Webong\Gateway\Laravel\RegistryRequestAuthenticator;
use Webong\Gateway\Laravel\RegistryRouteCache;
use Webong\Gateway\Planning\RoutePlannerRegistry;
use Webong\Proxy\EndpointDefinition;
use Webong\Proxy\EndpointRegistry;

final readonly class EndpointController
{
    public function __construct(
        private ValidationFactory $validator,
        private EndpointRegistry $endpoints,
        private RegistryRequestAuthenticator $authenticator,
        private RegistryRouteCache $routeCache,
        private RoutePlannerRegistry $routeKinds,
    ) {
    }

    public function __invoke(Request $request): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }

        $input = $request->json()->all();
        $validator = $this->validator->make($input, [
            'client' => ['required', 'string', 'max:255'],
            'external_id' => ['required', 'string', 'max:255'],
            'endpoint_key' => ['nullable', 'string', 'max:64', 'regex:/^[A-Za-z0-9_.-]+$/'],
            'signing_secret' => ['nullable', 'string', 'max:4096'],
            'verification_token' => ['nullable', 'string', 'max:4096'],
            'credential_owner_id' => ['required_unless:managed,true', 'nullable', 'string', 'max:255'],
            'managed' => ['sometimes', 'boolean'],
            'callback_url' => ['nullable', 'url:http,https', 'max:2048'],
            'registry' => ['nullable', 'string', 'max:255'],
            'metadata' => ['sometimes', 'array'],
            'route_kind' => ['sometimes', 'string', 'regex:/^[a-z][a-z0-9.-]{0,63}$/'],
        ]);

        if ($validator->fails()) {
            return response()->json([
                'message' => 'The endpoint is invalid.',
                'errors' => $validator->errors()->toArray(),
            ], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        if (array_key_exists('route_kind', $input)
            && $input['route_kind'] !== 'http'
            && ! $this->routeKinds->contains($input['route_kind'])) {
            return response()->json([
                'message' => 'The endpoint is invalid.',
                'errors' => ['route_kind' => ['The route kind is not installed.']],
            ], Response::HTTP_UNPROCESSABLE_ENTITY);
        }
        $gatewayInput = $input['metadata']['_gateway'] ?? null;
        if (is_array($gatewayInput) && array_key_exists('route_kind', $gatewayInput)) {
            return response()->json([
                'message' => 'The endpoint is invalid.',
                'errors' => ['route_kind' => ['Set route_kind as an endpoint property.']],
            ], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        try {
            $definition = new EndpointDefinition(
                client: $input['client'],
                externalId: $input['external_id'],
                signingSecret: $input['signing_secret'] ?? null,
                verificationToken: $input['verification_token'] ?? null,
                endpointKey: $input['endpoint_key'] ?? null,
                credentialOwnerId: $input['credential_owner_id'] ?? null,
                managed: (bool) ($input['managed'] ?? false),
                callbackUrl: $input['callback_url'] ?? null,
                registry: $input['registry'] ?? null,
                metadata: $input['metadata'] ?? [],
            );
            $previous = $this->endpoints->find($definition);
            $kind = $input['route_kind'] ?? ($previous !== null
                ? EndpointRouteKind::fromMetadata($previous->record->metadata)
                : ($this->routeKinds->contains('webhook') ? 'webhook' : 'http'));
            $metadata = $input['metadata'] ?? $previous?->record->metadata ?? [];
            $definition = new EndpointDefinition(
                client: $definition->client, externalId: $definition->externalId,
                signingSecret: $definition->signingSecret,
                verificationToken: $definition->verificationToken,
                endpointKey: $definition->endpointKey,
                credentialOwnerId: $definition->credentialOwnerId,
                managed: $definition->managed, callbackUrl: $definition->callbackUrl,
                registry: $definition->registry,
                metadata: EndpointRouteKind::withKind($metadata, $kind),
            );
            $endpoint = $this->endpoints->ensure($definition);
        } catch (InvalidArgumentException|RuntimeException $exception) {
            return response()->json([
                'message' => 'The endpoint is invalid.',
                'errors' => ['endpoint' => [$exception->getMessage()]],
            ], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        if ($previous !== null) {
            $this->routeCache->invalidateEndpoint($previous->record->endpoint_key);
        }
        $this->routeCache->invalidateEndpoint($endpoint->record->endpoint_key);
        $this->routeCache->invalidatePaths();

        return response()->json([
            'id' => $endpoint->record->id,
            'client' => $endpoint->record->client,
            'external_id' => $endpoint->record->external_id,
            'endpoint_key' => $endpoint->record->endpoint_key,
            'callback_url' => $endpoint->callbackUrl(),
            'managed' => $endpoint->record->is_managed,
            'active' => $endpoint->record->is_active,
            'route_kind' => EndpointRouteKind::fromMetadata($endpoint->record->metadata),
            'metadata' => EndpointRouteKind::publicMetadata($endpoint->record->metadata),
        ], Response::HTTP_CREATED);
    }
}
