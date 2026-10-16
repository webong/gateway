<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\DB;
use InvalidArgumentException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Exceptions\EndpointNotFoundException;
use Webong\Gateway\Protocol\MatchRules;

final readonly class SubscriptionController
{
    public function __construct(
        private ValidationFactory $validator,
        private SubscribeEndpoint $subscriptions,
        private RegistryRequestAuthenticator $authenticator,
        private ShellSshExecutionStore $sshExecutions,
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
            'target_kind',
            'hook',
            'ssh',
            'profile',
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
            'url' => ['sometimes', 'url:http,https'],
            'target_kind' => ['sometimes', 'in:http,shell'],
            'hook' => ['sometimes', 'string', 'min:1', 'max:1024'],
            'ssh' => ['sometimes', 'array'],
            'profile' => ['sometimes', 'string', 'min:1', 'max:64'],
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

        $shell = ($input['target_kind'] ?? 'http') === 'shell';
        if (array_key_exists('ssh', $input) && ! $shell) {
            return response()->json(['message' => 'The subscription is invalid.',
                'errors' => ['subscription' => ['SSH configuration requires a Shell reply subscription.']]],
                Response::HTTP_UNPROCESSABLE_ENTITY);
        }
        if (($shell && (! isset($input['hook']) || array_key_exists('url', $input)))
            || (! $shell && (array_key_exists('hook', $input) || ! isset($input['url'])))) {
            return response()->json([
                'message' => 'The subscription is invalid.',
                'errors' => ['subscription' => ['HTTP subscriptions require a URL; Shell subscriptions require a hook and no URL.']],
            ], Response::HTTP_UNPROCESSABLE_ENTITY);
        }
        if ($shell && ($authorization = $this->authenticator->authorizeShellMutation($request)) !== null) {
            return $authorization;
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
            $destination = DB::transaction(function () use ($input, $shell, $endpointKey, $match) {
                $sshExecution = isset($input['ssh']) ? $this->sshExecutions->create(
                    $endpointKey, $input['subscriber_id'], $input['hook'], $input['ssh'],
                ) : null;
                $definition = new SubscriptionDefinition(
                    subscriberId: $input['subscriber_id'],
                    subscriptionId: $input['subscription_id'] ?? $input['subscriber_id'],
                    webhookGroup: $input['webhook_group'],
                    routingScope: $input['routing_scope'],
                    routingKey: $input['routing_key'],
                    url: $input['url'] ?? '',
                    match: $match,
                    metadata: $input['metadata'] ?? [],
                    channel: $input['channel'] ?? null,
                    type: $input['type'] ?? 'relay',
                    profile: $input['profile'] ?? null,
                    shellHook: $shell ? $input['hook'] : null,
                    shellSshExecutionId: $sshExecution?->id,
                );

                return $this->subscriptions->handle($endpointKey, $definition);
            });
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
