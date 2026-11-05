<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel;

use Webong\Gateway\Contracts\PathResolver;
use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Planning\RoutePlannerRegistry;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\Response;
use Webong\Gateway\Protocols\RoutePlan;

/** Core HTTP ingress dispatch. Extensions own the behavior of named route kinds. */
final readonly class IngressRoutePlanner implements RoutePlanner
{
    public function __construct(
        private PathResolver $paths,
        private RegistryRouteCache $cache,
        private RoutePlannerRegistry $routes,
        private RegistryRoutePlanner $httpRoutes,
    ) {}

    public function plan(IngressRequest $request): RoutePlan
    {
        $binding = $request->binding ?? $this->cache->resolvePath($this->paths, $request);
        if ($binding === null) {
            return RoutePlan::passThrough();
        }

        $planner = $binding->kind === 'http'
            ? $this->httpRoutes
            : $this->routes->resolve($binding->kind);
        if ($planner === null) {
            return RoutePlan::respond(new Response(statusCode: 503, body: 'Route kind is unavailable.'));
        }

        return $planner->plan($request->withBinding($binding));
    }
}
