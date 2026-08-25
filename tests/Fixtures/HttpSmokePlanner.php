<?php

declare(strict_types=1);

namespace Webong\Gateway\Tests\Fixtures;

use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Protocol\Delivery;
use Webong\Gateway\Protocol\IngressRequest;
use Webong\Gateway\Protocol\Response;
use Webong\Gateway\Protocol\RoutePlan;
use Webong\Gateway\RegistryRoutePlanner;

final class HttpSmokePlanner implements RoutePlanner
{
    public function __construct(private readonly RegistryRoutePlanner $registryPlanner)
    {
    }

    public function plan(IngressRequest $request): RoutePlan
    {
        if ($request->path === '/http-smoke/registry-ingress') {
            return $this->registryPlanner->plan($request);
        }

        if ($request->path === '/http-smoke/relay') {
            $receiver = getenv('GATEWAY_HTTP_RECEIVER_URL');

            if (is_string($receiver) && $receiver !== '') {
                return RoutePlan::relay(relays: [new Delivery(
                    url: $receiver,
                    subscriberId: 'http-smoke-subscriber',
                )]);
            }
        }

        if ($request->path === '/http-smoke/plan') {
            return RoutePlan::respond(new Response(
                statusCode: 202,
                headers: ['X-Gateway-Plan' => ['http']],
                body: 'planned-by-http',
            ));
        }

        return RoutePlan::passThrough();
    }
}
