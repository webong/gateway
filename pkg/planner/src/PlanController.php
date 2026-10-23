<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Automation\CapabilityAwareRoutePlanner;
use Webong\Gateway\Protocols\IngressRequest;

final class PlanController
{
    public function __invoke(Request $request, CapabilityAwareRoutePlanner $planner): JsonResponse
    {
        $token = (string) config('gateway.internal_token', '');
        $provided = (string) $request->header('X-Gateway-Internal', '');

        if ($token === '' || $provided === '' || ! hash_equals($token, $provided)) {
            abort(Response::HTTP_NOT_FOUND);
        }

        $plan = $planner->plan(IngressRequest::fromArray($request->json()->all()));

        return response()->json($plan->toArray());
    }
}
