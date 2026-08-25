<?php

declare(strict_types=1);

namespace Webong\NetGateway;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Symfony\Component\HttpFoundation\Response;
use Webong\NetGateway\Exceptions\EndpointNotFoundException;
use Webong\NetGateway\Protocol\MatchRules;

final readonly class SubscriptionController
{
    public function __construct(
        private ValidationFactory $validator,
        private SubscribeEndpoint $subscriptions,
        private RegistryRequestAuthenticator $authenticator,
    ) {
    }

    public function __invoke(Request $request, string $endpointKey): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }

        $input = $request->json()->all();
        if (array_diff(array_keys($input), [
            'subscriber_id',
            'subscription_id',
            'type',
            'webhook_group',
            'routing_scope',
            'routing_key',
            'url',
            'channel',
            'metadata',
            'match',
        ]) !== []) {
            return response()->json([
                'message' => 'The subscription is invalid.',
                'errors' => ['subscription' => ['The request contains unsupported properties.']],
            ], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        $validator = $this->validator->make($input, [
            'subscriber_id' => ['required', 'string', 'max:255'],
            'subscription_id' => ['sometimes', 'string', 'max:255'],
            'type' => ['sometimes', 'in:relay,reply'],
            'webhook_group' => ['required', 'string', 'max:255'],
            'routing_scope' => ['required', 'string', 'max:255'],
            'routing_key' => ['required', 'string', 'max:255'],
            'url' => ['required', 'url:http,https'],
            'channel' => ['nullable', 'string', 'max:255'],
            'metadata' => ['sometimes', 'array'],
            'match' => ['sometimes', 'array'],
            'match.version' => ['required_with:match', 'string'],
            'match.rules' => ['required_with:match', 'array'],
        ]);

        if ($validator->fails()) {
            return response()->json([
                'message' => 'The subscription is invalid.',
                'errors' => $validator->errors()->toArray(),
            ], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        try {
            $match = array_key_exists('match', $input)
                ? MatchRules::fromArray($input['match'])
                : MatchRules::any();
        } catch (InvalidArgumentException $exception) {
            return response()->json([
                'message' => 'The subscription is invalid.',
                'errors' => ['match' => [$exception->getMessage()]],
            ], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        try {
            $definition = new SubscriptionDefinition(
                subscriberId: $input['subscriber_id'],
                subscriptionId: $input['subscription_id'] ?? $input['subscriber_id'],
                webhookGroup: $input['webhook_group'],
                routingScope: $input['routing_scope'],
                routingKey: $input['routing_key'],
                url: $input['url'],
                match: $match,
                metadata: $input['metadata'] ?? [],
                channel: $input['channel'] ?? null,
                type: $input['type'] ?? 'relay',
            );
            $destination = $this->subscriptions->handle($endpointKey, $definition);
        } catch (EndpointNotFoundException $exception) {
            return response()->json([
                'message' => $exception->getMessage(),
            ], Response::HTTP_NOT_FOUND);
        } catch (InvalidArgumentException $exception) {
            return response()->json([
                'message' => 'The subscription is invalid.',
                'errors' => ['subscription' => [$exception->getMessage()]],
            ], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        $resource = new SubscriptionResource($endpointKey, $destination);

        return response()->json($resource->toArray(), Response::HTTP_CREATED)
            ->header('ETag', $resource->etag());
    }

}
