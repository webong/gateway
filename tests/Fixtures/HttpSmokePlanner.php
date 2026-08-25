<?php

declare(strict_types=1);

namespace Webong\NetGateway\Tests\Fixtures;

use Webong\NetGateway\Contracts\RoutePlanner;
use Webong\NetGateway\Protocol\Delivery;
use Webong\NetGateway\Protocol\IngressRequest;
use Webong\NetGateway\Protocol\Response;
use Webong\NetGateway\Protocol\RoutePlan;
use Webong\NetGateway\RegistryRoutePlanner;

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
            $receiver = getenv('NET_GATEWAY_HTTP_RECEIVER_URL');

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
                headers: ['X-Net-Gateway-Plan' => ['http']],
                body: 'planned-by-http',
            ));
        }

        return RoutePlan::passThrough();
    }
}
