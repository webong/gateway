<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Accounts\EndpointAccountAccessStore;
use Webong\Gateway\Exceptions\EndpointNotFoundException;
use Webong\Gateway\Exceptions\SubscriptionLockUnavailableException;
use Webong\Gateway\Exceptions\SubscriptionNotFoundException;
use Webong\Gateway\Exceptions\SubscriptionRevisionMismatchException;
use Webong\Gateway\Protocols\SubscriptionState;

final readonly class SubscriptionManagementController
{
    public function __construct(
        private ValidationFactory $validator,
        private ManageSubscriptions $subscriptions,
        private RegistryRequestAuthenticator $authenticator,
        private EndpointAccountAccessStore $endpointAccess,
        private SubscriptionTargetExtensionRegistry $targetExtensions,
    ) {
    }

    public function index(Request $request, string $endpointKey): JsonResponse
    {
        $principal = $this->principal($request, $endpointKey);
        if ($principal instanceof JsonResponse) {
            return $principal;
        }

        $input = $request->query();
        $validator = $this->validator->make($input, [
            'routing_scope' => ['required', 'string', 'max:255'],
            'routing_key' => ['required', 'string', 'max:255'],
            'channel' => ['nullable', 'string', 'max:255'],
            'include_removed' => ['sometimes', 'in:true,false,1,0'],
        ]);
        if ($validator->fails()) {
            return $this->invalid('The subscription query is invalid.', $validator->errors()->toArray());
        }
        if (! $principal->isOperator()
            && ! $this->endpointAccess->allows(
                $endpointKey,
                (string) $principal->accountId,
                $input['routing_scope'],
                $input['routing_key'],
            )) {
            return $this->notFound();
        }

        try {
            $items = $this->subscriptions->route(
                endpointKey: $endpointKey,
                routingScope: $input['routing_scope'],
                routingKey: $input['routing_key'],
                channel: $input['channel'] ?? null,
                includeRemoved: filter_var($input['include_removed'] ?? false, FILTER_VALIDATE_BOOL),
            );
        } catch (EndpointNotFoundException $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_NOT_FOUND);
        } catch (InvalidArgumentException $exception) {
            return $this->invalid('The subscription query is invalid.', ['subscription' => [$exception->getMessage()]]);
        }

        if (! $principal->isOperator()) {
            $items = array_values(array_filter(
                $items,
                fn (SubscriptionResource $resource): bool => $this->owns($principal, $resource),
            ));
        }

        return response()->json(['data' => array_map(
            static fn (SubscriptionResource $resource): array => $resource->toArray(),
            $items,
        )]);
    }

    public function show(Request $request, string $endpointKey, string $destinationId): JsonResponse
    {
        $principal = $this->principal($request, $endpointKey);
        if ($principal instanceof JsonResponse) {
            return $principal;
        }

        try {
            $resource = $this->subscriptions->get($endpointKey, $destinationId, $this->channel($request));
            if (! $this->owns($principal, $resource)) {
                return $this->notFound();
            }

            return $this->resource($resource);
        } catch (EndpointNotFoundException|SubscriptionNotFoundException $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_NOT_FOUND);
        } catch (InvalidArgumentException $exception) {
            return $this->invalid('The subscription is invalid.', ['subscription' => [$exception->getMessage()]]);
        }
    }

    public function update(Request $request, string $endpointKey, string $destinationId): JsonResponse
    {
        $principal = $this->principal($request, $endpointKey);
        if ($principal instanceof JsonResponse) {
            return $principal;
        }

        $input = $request->json()->all();
        $channel = $input['channel'] ?? $this->channel($request);
        if (($denied = $this->authorizeResource($principal, $endpointKey, $destinationId, $channel)) !== null) {
            return $denied;
        }
        try {
            $current = $this->subscriptions->get($endpointKey, $destinationId, $channel);
            $extension = $this->targetExtensions->forDestination($current->destination);
        } catch (EndpointNotFoundException|SubscriptionNotFoundException $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_NOT_FOUND);
        } catch (InvalidArgumentException $exception) {
            return $this->invalid('The subscription update is invalid.', ['subscription' => [$exception->getMessage()]]);
        }
        $targetRules = $extension?->updateRules() ?? [];
        $allowed = ['url', 'profile', 'metadata', 'type', 'channel', ...array_keys($targetRules)];
        if (array_diff(array_keys($input), $allowed) !== []) {
            return $this->invalid('The subscription update is invalid.', [
                'subscription' => ['The request contains unsupported properties.'],
            ]);
        }
        $validator = $this->validator->make($input, [
            'url' => ['sometimes', 'url:http,https', 'max:2048'],
            'profile' => ['sometimes', 'nullable', 'string', 'min:1', 'max:64'],
            'metadata' => ['sometimes', 'array'],
            'type' => ['sometimes', 'in:relay,reply'],
            'channel' => ['nullable', 'string', 'max:255'],
            ...$targetRules,
        ]);
        if ($validator->fails()) {
            return $this->invalid('The subscription update is invalid.', $validator->errors()->toArray());
        }
        if (! $principal->isOperator() && isset($input['type']) && $input['type'] !== 'relay') {
            return response()->json(['message' => 'Account credentials can manage HTTP relay subscriptions only.'], Response::HTTP_FORBIDDEN);
        }

        try {
            if (($authorization = $this->authorizeTargetMutation($request, $endpointKey, $destinationId, $input['channel'] ?? null)) !== null) {
                return $authorization;
            }
            $expected = SubscriptionState::parseEtag($request->header('If-Match'));
            return $this->resource($this->subscriptions->update(
                endpointKey: $endpointKey,
                destinationId: $destinationId,
                expectedRevision: $expected,
                url: array_key_exists('url', $input) ? $input['url'] : null,
                metadata: array_key_exists('metadata', $input) ? $input['metadata'] : null,
                type: array_key_exists('type', $input) ? $input['type'] : null,
                profile: array_key_exists('profile', $input) ? $input['profile'] : null,
                profileProvided: array_key_exists('profile', $input),
                channel: $input['channel'] ?? null,
                targetChanges: array_diff_key($input, array_flip(['url', 'profile', 'metadata', 'type', 'channel'])),
            ));
        } catch (InvalidArgumentException $exception) {
            return $this->invalid('The subscription update is invalid.', ['subscription' => [$exception->getMessage()]]);
        } catch (EndpointNotFoundException|SubscriptionNotFoundException $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_NOT_FOUND);
        } catch (SubscriptionRevisionMismatchException $exception) {
            return $this->conflict($exception);
        } catch (SubscriptionLockUnavailableException $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_SERVICE_UNAVAILABLE);
        }
    }

    public function status(Request $request, string $endpointKey, string $destinationId): JsonResponse
    {
        $principal = $this->principal($request, $endpointKey);
        if ($principal instanceof JsonResponse) {
            return $principal;
        }

        $input = $request->json()->all();
        $validator = $this->validator->make($input, [
            'status' => ['required', 'in:active,paused'],
            'channel' => ['nullable', 'string', 'max:255'],
        ]);
        if (array_diff(array_keys($input), ['status', 'channel']) !== []) {
            return $this->invalid('The subscription status is invalid.', [
                'subscription' => ['The request contains unsupported properties.'],
            ]);
        }
        if ($validator->fails()) {
            return $this->invalid('The subscription status is invalid.', $validator->errors()->toArray());
        }

        try {
            if (($denied = $this->authorizeResource($principal, $endpointKey, $destinationId, $input['channel'] ?? null)) !== null) {
                return $denied;
            }
            if (($authorization = $this->authorizeTargetMutation($request, $endpointKey, $destinationId, $input['channel'] ?? null)) !== null) {
                return $authorization;
            }
            $expected = SubscriptionState::parseEtag($request->header('If-Match'));
            return $this->resource($this->subscriptions->setStatus(
                $endpointKey,
                $destinationId,
                $expected,
                $input['status'],
                $input['channel'] ?? null,
            ));
        } catch (InvalidArgumentException $exception) {
            return $this->invalid('The subscription status is invalid.', ['subscription' => [$exception->getMessage()]]);
        } catch (EndpointNotFoundException|SubscriptionNotFoundException $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_NOT_FOUND);
        } catch (SubscriptionRevisionMismatchException $exception) {
            return $this->conflict($exception);
        } catch (SubscriptionLockUnavailableException $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_SERVICE_UNAVAILABLE);
        }
    }

    public function destroy(Request $request, string $endpointKey, string $destinationId): Response
    {
        $principal = $this->principal($request, $endpointKey);
        if ($principal instanceof JsonResponse) {
            return $principal;
        }

        try {
            if (($denied = $this->authorizeResource($principal, $endpointKey, $destinationId, $this->channel($request))) !== null) {
                return $denied;
            }
            if (($authorization = $this->authorizeTargetMutation($request, $endpointKey, $destinationId, $this->channel($request))) !== null) {
                return $authorization;
            }
            $expected = SubscriptionState::parseEtag($request->header('If-Match'));
            $resource = $this->subscriptions->remove(
                $endpointKey,
                $destinationId,
                $expected,
                $this->channel($request),
            );

            return response()->noContent()->header('ETag', $resource->etag());
        } catch (InvalidArgumentException $exception) {
            return $this->invalid('The subscription removal is invalid.', ['subscription' => [$exception->getMessage()]]);
        } catch (EndpointNotFoundException|SubscriptionNotFoundException $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_NOT_FOUND);
        } catch (SubscriptionRevisionMismatchException $exception) {
            return $this->conflict($exception);
        } catch (SubscriptionLockUnavailableException $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_SERVICE_UNAVAILABLE);
        }
    }

    private function principal(Request $request, string $endpointKey): RegistryPrincipal|JsonResponse
    {
        $principal = $this->authenticator->authenticateRegistry($request);
        if ($principal instanceof JsonResponse) {
            return $principal;
        }
        return $principal;
    }

    private function owns(RegistryPrincipal $principal, SubscriptionResource $resource): bool
    {
        return $principal->isOperator()
            || ($resource->destination->owner_id === $principal->accountId
                && $this->targetExtensions->kindForDestination($resource->destination) === 'http'
                && $this->endpointAccess->allows(
                    $resource->endpointKey,
                    (string) $principal->accountId,
                    $resource->destination->routing_scope,
                    $resource->destination->routing_key,
                ));
    }

    private function authorizeResource(
        RegistryPrincipal $principal,
        string $endpointKey,
        string $destinationId,
        ?string $channel,
    ): ?JsonResponse {
        if ($principal->isOperator()) {
            return null;
        }

        try {
            $resource = $this->subscriptions->get($endpointKey, $destinationId, $channel);
        } catch (EndpointNotFoundException|SubscriptionNotFoundException) {
            return $this->notFound();
        }

        return $this->owns($principal, $resource) ? null : $this->notFound();
    }

    private function notFound(): JsonResponse
    {
        return response()->json(['message' => 'The subscription or endpoint was not found.'], Response::HTTP_NOT_FOUND);
    }

    private function channel(Request $request): ?string
    {
        $channel = $request->query('channel');

        return is_string($channel) && $channel !== '' ? $channel : null;
    }

    private function authorizeTargetMutation(Request $request, string $endpointKey, string $destinationId, ?string $channel): ?JsonResponse
    {
        $resource = $this->subscriptions->get($endpointKey, $destinationId, $channel);
        $extension = $this->targetExtensions->forDestination($resource->destination);

        return $extension?->authorizeMutation($request, $resource->destination);
    }

    private function resource(SubscriptionResource $resource): JsonResponse
    {
        return response()->json($resource->toArray())->header('ETag', $resource->etag());
    }

    /** @param array<string, mixed> $errors */
    private function invalid(string $message, array $errors): JsonResponse
    {
        $status = str_contains((string) data_get($errors, 'subscription.0'), 'If-Match')
            ? Response::HTTP_PRECONDITION_REQUIRED
            : Response::HTTP_UNPROCESSABLE_ENTITY;

        return response()->json(['message' => $message, 'errors' => $errors], $status);
    }

    private function conflict(SubscriptionRevisionMismatchException $exception): JsonResponse
    {
        return response()->json([
            'message' => $exception->getMessage(),
            'current_revision' => $exception->currentRevision,
        ], Response::HTTP_PRECONDITION_FAILED)->header(
            'ETag',
            SubscriptionState::etag($exception->currentRevision),
        );
    }
}
