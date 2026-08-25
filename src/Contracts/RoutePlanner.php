<?php

declare(strict_types=1);

namespace Webong\WebRelay\Contracts;

use Webong\WebRelay\Protocol\IngressRequest;
use Webong\WebRelay\Protocol\RoutePlan;

/**
 * The Laravel application implements this boundary.
 *
 * A planner validates the ingress request, resolves the agnostic endpoint /
 * subscriber registry, and binds the request to a reply or relay route. It
 * may use webong/web-proxy, but the bridge does not assume a tenant model or a
 * fixed public path.
 */
interface RoutePlanner
{
    public function plan(IngressRequest $request): RoutePlan;
}
