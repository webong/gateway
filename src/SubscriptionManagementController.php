<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Exceptions\EndpointNotFoundException;
use Webong\Gateway\Exceptions\SubscriptionLockUnavailableException;
use Webong\Gateway\Exceptions\SubscriptionNotFoundException;
use Webong\Gateway\Exceptions\SubscriptionRevisionMismatchException;
use Webong\Gateway\Protocol\SubscriptionState;

final readonly class SubscriptionManagementController
{
    public function __construct(
        private ValidationFactory $validator,
        private ManageSubscriptions $subscriptions,
        private RegistryRequestAuthenticator $authenticator,
    ) {
    }

    public function index(Request $request, string $endpointKey): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
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

        return response()->json(['data' => array_map(
            static fn (SubscriptionResource $resource): array => $resource->toArray(),
            $items,
        )]);
    }

    public function show(Request $request, string $endpointKey, string $destinationId): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }

        try {
            return $this->resource($this->subscriptions->get(
                $endpointKey,
                $destinationId,
                $this->channel($request),
            ));
        } catch (EndpointNotFoundException|SubscriptionNotFoundException $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_NOT_FOUND);
        } catch (InvalidArgumentException $exception) {
            return $this->invalid('The subscription is invalid.', ['subscription' => [$exception->getMessage()]]);
        }
    }

    public function update(Request $request, string $endpointKey, string $destinationId): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }

        $input = $request->json()->all();
        if (array_diff(array_keys($input), ['url', 'metadata', 'type', 'channel']) !== []) {
            return $this->invalid('The subscription update is invalid.', [
                'subscription' => ['The request contains unsupported properties.'],
            ]);
        }
        $validator = $this->validator->make($input, [
            'url' => ['sometimes', 'url:http,https', 'max:2048'],
            'metadata' => ['sometimes', 'array'],
            'type' => ['sometimes', 'in:relay,reply'],
            'channel' => ['nullable', 'string', 'max:255'],
        ]);
        if ($validator->fails()) {
            return $this->invalid('The subscription update is invalid.', $validator->errors()->toArray());
        }

        try {
            $expected = SubscriptionState::parseEtag($request->header('If-Match'));
            return $this->resource($this->subscriptions->update(
                endpointKey: $endpointKey,
                destinationId: $destinationId,
                expectedRevision: $expected,
                url: array_key_exists('url', $input) ? $input['url'] : null,
                metadata: array_key_exists('metadata', $input) ? $input['metadata'] : null,
                type: array_key_exists('type', $input) ? $input['type'] : null,
                channel: $input['channel'] ?? null,
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
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
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
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }

        try {
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

    private function channel(Request $request): ?string
    {
        $channel = $request->query('channel');

        return is_string($channel) && $channel !== '' ? $channel : null;
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
