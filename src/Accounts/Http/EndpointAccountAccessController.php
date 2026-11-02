<?php

declare(strict_types=1);

namespace Webong\Gateway\Accounts\Http;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Accounts\EndpointAccountAccessStore;
use Webong\Gateway\Exceptions\EndpointNotFoundException;
use Webong\Gateway\RegistryRequestAuthenticator;

final readonly class EndpointAccountAccessController
{
    public function __construct(
        private ValidationFactory $validator,
        private RegistryRequestAuthenticator $operator,
        private EndpointAccountAccessStore $access,
    ) {
    }

    public function grant(Request $request, string $endpointKey, string $accountId): JsonResponse
    {
        if (($denied = $this->operator->authorize($request)) !== null) {
            return $denied;
        }
        $input = $request->json()->all();
        $validator = $this->validator->make($input, [
            'routing_scope' => ['required', 'string', 'max:255'],
            'routing_key' => ['required', 'string', 'max:255'],
        ]);
        if (array_diff(array_keys($input), ['routing_scope', 'routing_key']) !== [] || $validator->fails()) {
            return response()->json(['message' => 'The endpoint access grant is invalid.'], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        try {
            $grant = $this->access->grant($endpointKey, $accountId, $input['routing_scope'], $input['routing_key']);
        } catch (EndpointNotFoundException $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_NOT_FOUND);
        } catch (\InvalidArgumentException $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        return response()->json([
            'endpoint_key' => $endpointKey,
            'account_id' => $accountId,
            'routing_scope' => $input['routing_scope'],
            'routing_key' => $input['routing_key'],
            'enabled' => $grant->enabled,
            'revision' => $grant->revision,
        ]);
    }

    public function revoke(Request $request, string $endpointKey, string $accountId): Response
    {
        if (($denied = $this->operator->authorize($request)) !== null) {
            return $denied;
        }
        $input = $request->json()->all();
        $validator = $this->validator->make($input, [
            'routing_scope' => ['required', 'string', 'max:255'],
            'routing_key' => ['required', 'string', 'max:255'],
        ]);
        if (array_diff(array_keys($input), ['routing_scope', 'routing_key']) !== [] || $validator->fails()) {
            return response()->json(['message' => 'The endpoint access grant is invalid.'], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        $this->access->revoke($endpointKey, $accountId, $input['routing_scope'], $input['routing_key']);

        return response()->noContent();
    }
}
