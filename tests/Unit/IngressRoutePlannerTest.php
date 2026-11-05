<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\PathResolver;
use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Laravel\IngressRoutePlanner;
use Webong\Gateway\Laravel\RegistryRouteCache;
use Webong\Gateway\Planning\RoutePlannerRegistry;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\PathBinding;
use Webong\Gateway\Protocols\Response;
use Webong\Gateway\Protocols\RoutePlan;

function ingressRouteRequest(): IngressRequest
{
    return new IngressRequest(
        deliveryId: 'delivery-1', method: 'POST', host: 'example.test', path: '/hook',
        rawQuery: '', headers: [], body: 'payload',
    );
}

it('passes unbound paths through without invoking Webhook', function (): void {
    $handler = new class implements RoutePlanner {
        public int $calls = 0;
        public function plan(IngressRequest $request): RoutePlan { $this->calls++; return RoutePlan::respond(new Response(200)); }
    };
    $routes = new RoutePlannerRegistry();
    $routes->register('webhook', fn (): RoutePlanner => $handler);
    foreach ([null] as $binding) {
        $paths = new class($binding) implements PathResolver {
            public function __construct(private ?PathBinding $binding) {}
            public function resolve(IngressRequest $request): ?PathBinding { return $this->binding; }
        };
        $planner = new IngressRoutePlanner($paths, app(RegistryRouteCache::class), $routes, app(\Webong\Gateway\Laravel\RegistryRoutePlanner::class));
        expect($planner->plan(ingressRouteRequest())->action)->toBe(RoutePlan::PASS_THROUGH);
    }
    expect($handler->calls)->toBe(0);
});

it('uses core registry routing for plain HTTP bindings', function (): void {
    $binding = new PathBinding('missing-endpoint', kind: 'http');
    $paths = new class($binding) implements PathResolver {
        public function __construct(private PathBinding $binding) {}
        public function resolve(IngressRequest $request): ?PathBinding { return $this->binding; }
    };
    $planner = new IngressRoutePlanner($paths, app(RegistryRouteCache::class), new RoutePlannerRegistry(), app(\Webong\Gateway\Laravel\RegistryRoutePlanner::class));
    $plan = $planner->plan(ingressRouteRequest());
    expect($plan->action)->toBe(RoutePlan::RESPOND)
        ->and($plan->immediateResponse?->statusCode)->toBe(404);
});

it('dispatches a Webhook binding once and fails closed when its owner is absent', function (): void {
    $paths = new class implements PathResolver {
        public int $resolves = 0;
        public function resolve(IngressRequest $request): ?PathBinding
        {
            $this->resolves++;
            return new PathBinding('endpoint', kind: 'webhook');
        }
    };
    $handler = new class implements RoutePlanner {
        public int $calls = 0;
        public function plan(IngressRequest $request): RoutePlan
        {
            $this->calls++;
            expect($request->binding?->kind)->toBe('webhook');
            return RoutePlan::respond(new Response(statusCode: 202));
        }
    };
    $routes = new RoutePlannerRegistry();
    $routes->register('webhook', fn (): RoutePlanner => $handler);
    $planner = new IngressRoutePlanner($paths, app(RegistryRouteCache::class), $routes, app(\Webong\Gateway\Laravel\RegistryRoutePlanner::class));
    expect($planner->plan(ingressRouteRequest())->immediateResponse?->statusCode)->toBe(202)
        ->and($paths->resolves)->toBe(1)->and($handler->calls)->toBe(1);

    $unavailable = new IngressRoutePlanner($paths, app(RegistryRouteCache::class), new RoutePlannerRegistry(), app(\Webong\Gateway\Laravel\RegistryRoutePlanner::class));
    expect($unavailable->plan(ingressRouteRequest())->immediateResponse?->statusCode)->toBe(503);
});
