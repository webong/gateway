<?php

declare(strict_types=1);

namespace Webong\Gateway\Shell;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Response;

final class ShellOperatorAuthenticator
{
    public function authorize(Request $request): ?JsonResponse
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
