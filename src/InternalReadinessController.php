<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Illuminate\Http\Request;
use Illuminate\Http\Response;

final class InternalReadinessController
{
    public function __invoke(Request $request, PlannerReadiness $readiness): Response
    {
        $token = (string) config('gateway.internal_token', '');
        $provided = (string) $request->header('X-Gateway-Internal', '');
        if ($token === '' || $provided === '' || ! hash_equals($token, $provided)) {
            abort(404);
        }

        return response()->noContent($readiness->ready() ? 204 : 503);
    }
}
