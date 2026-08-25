<?php

declare(strict_types=1);

namespace Webong\WebRelay;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Symfony\Component\HttpFoundation\Response;
use Webong\WebRelay\Exceptions\EndpointNotFoundException;
use Webong\WebRelay\Exceptions\SubscriptionLockUnavailableException;
use Webong\WebRelay\Exceptions\SubscriptionNotFoundException;
use Webong\WebRelay\Exceptions\SubscriptionRevisionMismatchException;
use Webong\WebRelay\Protocol\MatchRulesPatch;
use Webong\WebRelay\Protocol\SubscriptionState;

final readonly class SubscriptionMatchController
{
    public function __construct(
        private ValidationFactory $validator,
        private PatchSubscriptionMatch $subscriptions,
        private RegistryRequestAuthenticator $authenticator,
    ) {
    }

    public function __invoke(Request $request, string $endpointKey, string $destinationId): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
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
