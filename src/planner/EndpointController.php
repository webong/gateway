<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use RuntimeException;
use Symfony\Component\HttpFoundation\Response;
use Webong\WebProxy\EndpointDefinition;
use Webong\WebProxy\EndpointRegistry;

final readonly class EndpointController
{
    public function __construct(
        private ValidationFactory $validator,
        private EndpointRegistry $endpoints,
        private RegistryRequestAuthenticator $authenticator,
        private RegistryRouteCache $routeCache,
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
        ]);

        if ($validator->fails()) {
            return response()->json([
                'message' => 'The endpoint is invalid.',
                'errors' => $validator->errors()->toArray(),
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
            'metadata' => $endpoint->record->metadata,
        ], Response::HTTP_CREATED);
    }
}
