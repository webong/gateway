<?php

declare(strict_types=1);

namespace Webong\Gateway\Reconciliation;

use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Response;

final class InternalRequestAuthenticator
{
    public function authorizeOrHide(Request $request): void
    {
        $token = (string) config('gateway.internal_token', '');
        $provided = (string) $request->header('X-Gateway-Internal', '');

        if ($token === '' || $provided === '' || ! hash_equals($token, $provided)) {
            abort(Response::HTTP_NOT_FOUND);
        }
    }
}
