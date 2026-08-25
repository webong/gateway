<?php

declare(strict_types=1);

namespace Webong\WebRelay\Tests\Fixtures;

use Webong\WebRelay\Contracts\RoutePlanner;
use Webong\WebRelay\Protocol\Delivery;
use Webong\WebRelay\Protocol\IngressRequest;
use Webong\WebRelay\Protocol\Response;
use Webong\WebRelay\Protocol\RoutePlan;

final class HttpSmokePlanner implements RoutePlanner
{
    public function plan(IngressRequest $request): RoutePlan
    {
        if ($request->path === '/http-smoke/relay') {
            $receiver = getenv('WEB_RELAY_HTTP_RECEIVER_URL');

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
                headers: ['X-Web-Relay-Plan' => ['http']],
                body: 'planned-by-http',
            ));
        }

        return RoutePlan::passThrough();
    }
}
