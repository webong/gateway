<?php

declare(strict_types=1);

namespace Webong\NetGateway;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Response;
use Webong\NetGateway\Contracts\RoutePlanner;
use Webong\NetGateway\Protocol\IngressRequest;

final class PlanController
{
    public function __invoke(Request $request, RoutePlanner $planner): JsonResponse
    {
        $token = (string) config('net-gateway.internal_token', '');
        $provided = (string) $request->header('X-Net-Gateway-Internal', '');

        if ($token === '' || $provided === '' || ! hash_equals($token, $provided)) {
            abort(Response::HTTP_NOT_FOUND);
        }

        $plan = $planner->plan(IngressRequest::fromArray($request->json()->all()));

        return response()->json($plan->toArray());
    }
}
