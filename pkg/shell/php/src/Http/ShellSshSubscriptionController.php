<?php

declare(strict_types=1);

namespace Webong\Gateway\Shell\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Exceptions\EndpointNotFoundException;
use Webong\Gateway\Exceptions\SubscriptionLockUnavailableException;
use Webong\Gateway\Exceptions\SubscriptionNotFoundException;
use Webong\Gateway\Exceptions\SubscriptionRevisionMismatchException;
use Webong\Gateway\Laravel\RegistryRequestAuthenticator;
use Webong\Gateway\Laravel\ResolvedSubscription;
use Webong\Gateway\Laravel\SubscriptionRegistry;
use Webong\Gateway\Protocols\SubscriptionState;
use Webong\Gateway\Shell\ShellOperatorAuthenticator;
use Webong\Gateway\Shell\ShellSshExecutionStore;
use Webong\Gateway\Shell\ShellSubscriptionExtension;

final readonly class ShellSshSubscriptionController
{
    public function __construct(
        private SubscriptionRegistry $subscriptions,
        private RegistryRequestAuthenticator $registry,
        private ShellOperatorAuthenticator $operator,
        private ShellSubscriptionExtension $extension,
    ) {
    }

    public function __invoke(Request $request, string $endpointKey, string $destinationId): JsonResponse
    {
        if (($denied = $this->registry->authorize($request) ?? $this->operator->authorize($request)) !== null) {
            return $denied;
        }
        $input = $request->json()->all();
        if (array_keys($input) !== ['private_key'] || ! is_string($input['private_key'])) {
            return response()->json([
                'message' => 'The SSH credential is invalid.',
                'errors' => ['subscription' => ['A private_key is required.']],
            ], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        try {
            ShellSshExecutionStore::assertPrivateKey($input['private_key']);
            $resource = $this->subscriptions->mutate(
                $endpointKey,
                $destinationId,
                SubscriptionState::parseEtag($request->header('If-Match')),
                function (ResolvedSubscription $resolved) use ($input): array {
                    if (! $this->extension->matchesTarget($resolved->destination->target)) {
                        throw new InvalidArgumentException('The subscription has no active SSH credential.');
                    }
                    $this->extension->rotateCredential($resolved, $input['private_key']);

                    return ['metadata' => $resolved->destination->metadata];
                },
            );

            return response()->json($resource->toArray())->header('ETag', $resource->etag());
        } catch (InvalidArgumentException $exception) {
            return response()->json([
                'message' => 'The SSH credential is invalid.',
                'errors' => ['subscription' => [$exception->getMessage()]],
            ], str_contains($exception->getMessage(), 'If-Match')
                ? Response::HTTP_PRECONDITION_REQUIRED : Response::HTTP_UNPROCESSABLE_ENTITY);
        } catch (EndpointNotFoundException|SubscriptionNotFoundException $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_NOT_FOUND);
        } catch (SubscriptionRevisionMismatchException $exception) {
            return response()->json([
                'message' => $exception->getMessage(),
                'current_revision' => $exception->currentRevision,
            ], Response::HTTP_PRECONDITION_FAILED)->header(
                'ETag',
                SubscriptionState::etag($exception->currentRevision),
            );
        } catch (SubscriptionLockUnavailableException $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_SERVICE_UNAVAILABLE);
        }
    }
}
