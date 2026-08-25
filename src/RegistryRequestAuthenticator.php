<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Response;

final class RegistryRequestAuthenticator
{
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
}
