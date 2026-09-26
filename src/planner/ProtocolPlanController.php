<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Protocol\GatewayEvent;

final class ProtocolPlanController
{
    public function __invoke(Request $request, ProtocolPlanner $planner): JsonResponse
    {
        $token = (string) config('gateway.internal_token', '');
        $provided = (string) $request->header('X-Gateway-Internal', '');

        if ($token === '' || $provided === '' || ! hash_equals($token, $provided)) {
            abort(Response::HTTP_NOT_FOUND);
        }

        $decision = $planner->planEvent(GatewayEvent::fromArray($request->json()->all()));

        return response()->json($decision->toArray());
    }
}
