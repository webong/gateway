<?php

declare(strict_types=1);

namespace Webong\WebRelay;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Response;
use Webong\WebRelay\Contracts\RoutePlanner;
use Webong\WebRelay\Protocol\IngressRequest;

final class PlanController
{
    public function __invoke(Request $request, RoutePlanner $planner): JsonResponse
    {
        $token = (string) config('web-relay.internal_token', '');
        $provided = (string) $request->header(
            'X-Web-Relay-Internal',
            $request->header('X-RoadRunner-Relay-Internal', ''),
        );

        if ($token === '' || $provided === '' || ! hash_equals($token, $provided)) {
            abort(Response::HTTP_NOT_FOUND);
        }

        $plan = $planner->plan(IngressRequest::fromArray($request->json()->all()));

        return response()->json($plan->toArray());
    }
}
