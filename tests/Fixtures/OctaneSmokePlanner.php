<?php

declare(strict_types=1);

namespace Webong\WebRelay\Tests\Fixtures;

use Webong\WebRelay\Contracts\RoutePlanner;
use Webong\WebRelay\Protocol\IngressRequest;
use Webong\WebRelay\Protocol\Response;
use Webong\WebRelay\Protocol\RoutePlan;

final class OctaneSmokePlanner implements RoutePlanner
{
    public function plan(IngressRequest $request): RoutePlan
    {
        if ($request->path === '/octane-smoke/plan') {
            return RoutePlan::respond(new Response(
                statusCode: 202,
                headers: ['X-Web-Relay-Plan' => ['octane']],
                body: 'planned-by-octane',
            ));
        }

        return RoutePlan::passThrough();
    }
}
