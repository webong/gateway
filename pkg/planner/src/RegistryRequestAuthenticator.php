<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Illuminate\Http\JsonResponse;
use Webong\Gateway\Accounts\AccountStore;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Response;

final class RegistryRequestAuthenticator
{
    public function __construct(private readonly AccountStore $accounts)
    {
    }

    public function authenticateRegistry(Request $request): RegistryPrincipal|JsonResponse
    {
        $provided = (string) $request->bearerToken();
        if ($provided === '') {
            return $this->unauthenticated();
        }

        $operatorToken = (string) config('gateway.registry_token', '');
        if ($operatorToken !== '' && hash_equals($operatorToken, $provided)) {
            return RegistryPrincipal::operator();
        }

        $accountId = $this->accounts->idForToken($provided);

        return $accountId === null
            ? $this->unauthenticated()
            : RegistryPrincipal::account($accountId);
    }

    public function authorize(Request $request): ?JsonResponse
    {
        $token = (string) config('gateway.registry_token', '');

        if ($token === '') {
            return response()->json([
                'message' => 'The registry API is not configured.',
            ], Response::HTTP_SERVICE_UNAVAILABLE);
        }

        $provided = (string) $request->bearerToken();
        if ($provided === '' || ! hash_equals($token, $provided)) {
            return response()->json([
                'message' => 'Unauthenticated.',
            ], Response::HTTP_UNAUTHORIZED);
        }

        return null;
    }

    private function unauthenticated(): JsonResponse
    {
        return response()->json([
            'message' => 'Unauthenticated.',
        ], Response::HTTP_UNAUTHORIZED);
    }

}
