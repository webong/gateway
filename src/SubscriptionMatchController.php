<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Webong\Gateway\Accounts\EndpointAccountAccessStore;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Exceptions\EndpointNotFoundException;
use Webong\Gateway\Exceptions\SubscriptionLockUnavailableException;
use Webong\Gateway\Exceptions\SubscriptionNotFoundException;
use Webong\Gateway\Exceptions\SubscriptionRevisionMismatchException;
use Webong\Gateway\Protocols\MatchRulesPatch;
use Webong\Gateway\Protocols\SubscriptionState;

final readonly class SubscriptionMatchController
{
    public function __construct(
        private ValidationFactory $validator,
        private PatchSubscriptionMatch $subscriptions,
        private ManageSubscriptions $subscriptionReader,
        private RegistryRequestAuthenticator $authenticator,
        private EndpointAccountAccessStore $endpointAccess,
        private SubscriptionTargetExtensionRegistry $targetExtensions,
    ) {
    }

    public function __invoke(Request $request, string $endpointKey, string $destinationId): JsonResponse
    {
        $principal = $this->authenticator->authenticateRegistry($request);
        if ($principal instanceof JsonResponse) {
            return $principal;
        }
        $input = $request->json()->all();
        $validator = $this->validator->make($input, [
            'version' => ['required', 'string'],
            'operations' => ['required', 'array', 'min:1', 'max:128'],
            'channel' => ['nullable', 'string', 'max:255'],
        ]);

        if ($validator->fails()) {
            return response()->json([
                'message' => 'The match patch is invalid.',
                'errors' => $validator->errors()->toArray(),
            ], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        try {
            $resource = $this->subscriptionReader->get($endpointKey, $destinationId, $input['channel'] ?? null);
            $extension = $this->targetExtensions->forDestination($resource->destination);
            if (! $principal->isOperator()
                && ($resource->destination->owner_id !== $principal->accountId
                    || ($extension !== null && $extension->kind() !== 'http')
                    || ! $this->endpointAccess->allows(
                        $endpointKey,
                        (string) $principal->accountId,
                        $resource->destination->routing_scope,
                        $resource->destination->routing_key,
                    ))) {
                return response()->json(['message' => 'The subscription was not found.'], Response::HTTP_NOT_FOUND);
            }
            if ($extension !== null
                && ($authorization = $extension->authorizeMutation($request, $resource->destination)) !== null) {
                return $authorization;
            }
            $expectedRevision = SubscriptionState::parseEtag($request->header('If-Match'));
            $patchPayload = $input;
            unset($patchPayload['channel']);
            $patch = MatchRulesPatch::fromArray($patchPayload);
            $resource = $this->subscriptions->handle(
                endpointKey: $endpointKey,
                destinationId: $destinationId,
                patch: $patch,
                expectedRevision: $expectedRevision,
                channel: $input['channel'] ?? null,
            );
        } catch (EndpointNotFoundException|SubscriptionNotFoundException $exception) {
            return response()->json([
                'message' => $exception->getMessage(),
            ], Response::HTTP_NOT_FOUND);
        } catch (InvalidArgumentException $exception) {
            $status = str_contains($exception->getMessage(), 'If-Match')
                ? Response::HTTP_PRECONDITION_REQUIRED
                : Response::HTTP_UNPROCESSABLE_ENTITY;

            return response()->json([
                'message' => 'The match patch is invalid.',
                'errors' => ['match' => [$exception->getMessage()]],
            ], $status);
        } catch (SubscriptionRevisionMismatchException $exception) {
            return response()->json([
                'message' => $exception->getMessage(),
                'current_revision' => $exception->currentRevision,
            ], Response::HTTP_PRECONDITION_FAILED)->header(
                'ETag',
                SubscriptionState::etag($exception->currentRevision),
            );
        } catch (SubscriptionLockUnavailableException $exception) {
            return response()->json([
                'message' => $exception->getMessage(),
            ], Response::HTTP_SERVICE_UNAVAILABLE);
        }

        return response()->json($resource->toArray())->header('ETag', $resource->etag());
    }
}
