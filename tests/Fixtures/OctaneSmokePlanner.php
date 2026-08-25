<?php

declare(strict_types=1);

namespace Webong\NetGateway\Tests\Fixtures;

use Webong\NetGateway\Contracts\RoutePlanner;
use Webong\NetGateway\Protocol\IngressRequest;
use Webong\NetGateway\Protocol\Response;
use Webong\NetGateway\Protocol\RoutePlan;

final class OctaneSmokePlanner implements RoutePlanner
{
    public function plan(IngressRequest $request): RoutePlan
    {
        if ($request->path === '/octane-smoke/plan') {
            return RoutePlan::respond(new Response(
                statusCode: 202,
                headers: ['X-Net-Gateway-Plan' => ['octane']],
                body: 'planned-by-octane',
            ));
        }

        return RoutePlan::passThrough();
    }
}
