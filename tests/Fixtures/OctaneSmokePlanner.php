<?php

declare(strict_types=1);

namespace Webong\Gateway\Tests\Fixtures;

use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Protocol\IngressRequest;
use Webong\Gateway\Protocol\Response;
use Webong\Gateway\Protocol\RoutePlan;

final class OctaneSmokePlanner implements RoutePlanner
{
    public function plan(IngressRequest $request): RoutePlan
    {
        if ($request->path === '/octane-smoke/plan') {
            return RoutePlan::respond(new Response(
                statusCode: 202,
                headers: ['X-Gateway-Plan' => ['octane']],
                body: 'planned-by-octane',
            ));
        }

        return RoutePlan::passThrough();
    }
}
