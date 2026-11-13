<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel\Http;

use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\JsonResponse;
use Webong\Gateway\Automation\CapabilityAwareRoutePlanner;
use Webong\Gateway\Planning\PlanningEndpoint;

final class PlanController
{
    public function __invoke(Request $request, CapabilityAwareRoutePlanner $planner): JsonResponse
    {
        $result = (new PlanningEndpoint(
            (string) config('gateway.internal_token', ''),
            (int) config('gateway.max_planning_body_bytes', 1048576),
        ))->plan($request->getMethod(), $request->headers->get('X-Gateway-Internal', ''), $request->getContent(), $planner);

        return new JsonResponse($result->payload, $result->status, $result->headers);
    }
}
