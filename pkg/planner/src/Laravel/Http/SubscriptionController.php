<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel\Http;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\DB;
use InvalidArgumentException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Exceptions\EndpointNotFoundException;
use Webong\Gateway\Laravel\Accounts\EndpointAccountAccessStore;
use Webong\Gateway\Laravel\RegistryRequestAuthenticator;
use Webong\Gateway\Laravel\SubscribeEndpoint;
use Webong\Gateway\Laravel\SubscriptionDefinition;
use Webong\Gateway\Laravel\SubscriptionResource;
use Webong\Gateway\Laravel\SubscriptionTargetExtensionRegistry;
use Webong\Gateway\Protocols\MatchRules;

final readonly class SubscriptionController
{
    public function __construct(
        private ValidationFactory $validator,
        private SubscribeEndpoint $subscriptions,
        private RegistryRequestAuthenticator $authenticator,
        private EndpointAccountAccessStore $endpointAccess,
        private SubscriptionTargetExtensionRegistry $targetExtensions,
    ) {}

    public function __invoke(Request $request, string $endpointKey): JsonResponse
    {
        $principal = $this->authenticator->authenticateRegistry($request);
        if ($principal instanceof JsonResponse) { return $principal; }
        $input = $request->json()->all();
        $targetKind = $input['target_kind'] ?? $this->targetExtensions->defaultKind();
        if (! is_string($targetKind) || $targetKind === '') {
            return $this->invalid(['target_kind' => ['Specify a target kind or install a default target extension.']]);
        }

        try { $extension = $this->targetExtensions->forKind($targetKind); }
        catch (InvalidArgumentException $exception) { return $this->invalid(['target_kind' => [$exception->getMessage()]]); }

        $targetRules = $extension->creationRules();
        $allowed = [
            'subscriber_id', 'subscription_id', 'type', 'group', 'routing_scope',
            'routing_key', 'target_kind', 'channel', 'metadata', 'match', ...array_keys($targetRules),
        ];
        if (array_diff(array_keys($input), $allowed) !== []) {
            return response()->json([
                'message' => 'The subscription is invalid.',
                'errors' => ['subscription' => ['The request contains unsupported properties.']],
            ], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        $rules = [
            'subscriber_id' => $principal->isOperator() ? ['required', 'string', 'max:255'] : ['sometimes', 'string', 'max:255'],
            'subscription_id' => ['sometimes', 'string', 'max:255'],
            'type' => ['sometimes', 'in:relay,reply'],
            'group' => ['required', 'string', 'max:255'],
            'routing_scope' => ['required', 'string', 'max:255'],
            'routing_key' => ['required', 'string', 'max:255'],
            'target_kind' => ['sometimes', 'string', 'max:64'],
            'channel' => ['nullable', 'string', 'max:255'],
            'metadata' => ['sometimes', 'array'],
            'match' => ['sometimes', 'array'],
            'match.version' => ['required_with:match', 'string'],
            'match.rules' => ['required_with:match', 'array'],
            ...$targetRules,
        ];
        $validator = $this->validator->make($input, $rules);
        if ($validator->fails()) { return $this->invalid($validator->errors()->toArray()); }

        $type = $input['type'] ?? 'relay';
        if (! $principal->isOperator()) {
            if (! $this->endpointAccess->allows($endpointKey, (string) $principal->accountId, $input['routing_scope'], $input['routing_key'])) {
                return response()->json(['message' => 'The endpoint route was not found.'], Response::HTTP_NOT_FOUND);
            }
            if (isset($input['subscriber_id']) && $input['subscriber_id'] !== $principal->accountId) {
                return response()->json(['message' => 'The subscriber identity is assigned by Gateway.'], Response::HTTP_FORBIDDEN);
            }
            if (! $extension->allowsAccountCredentials('create', $type)) {
                return response()->json(['message' => 'Account credentials cannot create this subscription target.'], Response::HTTP_FORBIDDEN);
            }
            $input['subscriber_id'] = $principal->accountId;
        }
        if (($authorization = $extension->authorizeCreate($request)) !== null) { return $authorization; }

        try {
            $match = array_key_exists('match', $input) ? MatchRules::fromArray($input['match']) : MatchRules::any();
        } catch (InvalidArgumentException $exception) { return $this->invalid(['match' => [$exception->getMessage()]]); }

        try {
            $destination = DB::transaction(function () use ($input, $targetKind, $extension, $endpointKey, $match, $type) {
                $target = $extension->createTarget($input, $endpointKey, $input['subscriber_id']);
                $definition = new SubscriptionDefinition(
                    subscriberId: $input['subscriber_id'],
                    subscriptionId: $input['subscription_id'] ?? $input['subscriber_id'],
                    group: $input['group'],
                    routingScope: $input['routing_scope'],
                    routingKey: $input['routing_key'],
                    targetKind: $targetKind,
                    target: $target,
                    match: $match,
                    metadata: $input['metadata'] ?? [],
                    targetOptions: $extension->createTargetOptions($input),
                    channel: $input['channel'] ?? null,
                    type: $type,
                );

                return $this->subscriptions->handle($endpointKey, $definition);
            });
        } catch (EndpointNotFoundException $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_NOT_FOUND);
        } catch (InvalidArgumentException $exception) {
            return $this->invalid(['subscription' => [$exception->getMessage()]]);
        }

        $resource = new SubscriptionResource($endpointKey, $destination, $this->targetExtensions);

        return response()->json($resource->toArray(), Response::HTTP_CREATED)->header('ETag', $resource->etag());
    }

    /** @param array<string, mixed> $errors */
    private function invalid(array $errors): JsonResponse
    {
        return response()->json(['message' => 'The subscription is invalid.', 'errors' => $errors], Response::HTTP_UNPROCESSABLE_ENTITY);
    }
}
