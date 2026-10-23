<?php

declare(strict_types=1);

namespace Webong\Gateway\Tests\Fixtures;

use Webong\Gateway\Contract\RoutePlanner;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\Response;
use Webong\Gateway\Protocols\RoutePlan;

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
