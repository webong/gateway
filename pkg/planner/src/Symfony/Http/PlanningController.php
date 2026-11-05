<?php

declare(strict_types=1);

namespace Webong\Gateway\Symfony\Http;

use Symfony\Component\HttpFoundation\JsonResponse;
use Symfony\Component\HttpFoundation\Request;
use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Planning\PlanningEndpoint;

final readonly class PlanningController
{
    public function __construct(
        private PlanningEndpoint $endpoint,
        private RoutePlanner $planner,
        private ProtocolPlanner $protocolPlanner,
    ) {}

    public function plan(Request $request): JsonResponse
    {
        $result = $this->endpoint->plan($request->getMethod(), $request->headers->get('X-Gateway-Internal', ''), $request->getContent(), $this->planner);

        return new JsonResponse($result->payload, $result->status, $result->headers);
    }

    public function event(Request $request): JsonResponse
    {
        $result = $this->endpoint->event($request->getMethod(), $request->headers->get('X-Gateway-Internal', ''), $request->getContent(), $this->protocolPlanner);

        return new JsonResponse($result->payload, $result->status, $result->headers);
    }
}
