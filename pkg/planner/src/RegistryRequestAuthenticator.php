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

    public function authorizeShellMutation(Request $request): ?JsonResponse
    {
        $token = (string) config('gateway.shell.subscription_token', '');
        if ($token === '') {
            return response()->json([
                'message' => 'Shell subscription management is not configured.',
            ], Response::HTTP_SERVICE_UNAVAILABLE);
        }

        $provided = (string) $request->header('X-Gateway-Shell-Subscription', '');
        if ($provided === '' || ! hash_equals($token, $provided)) {
            return response()->json([
                'message' => 'Shell subscription management is forbidden.',
            ], Response::HTTP_FORBIDDEN);
        }

        return null;
    }
}
