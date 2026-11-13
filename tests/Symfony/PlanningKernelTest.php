<?php

declare(strict_types=1);

namespace Webong\Gateway\Tests\Symfony;

use PHPUnit\Framework\TestCase;
use Symfony\Component\DependencyInjection\ContainerBuilder;
use Symfony\Component\EventDispatcher\EventDispatcher;
use Symfony\Component\HttpFoundation\Request;
use Symfony\Component\HttpFoundation\RequestStack;
use Symfony\Component\HttpKernel\Controller\ArgumentResolver;
use Symfony\Component\HttpKernel\Controller\ContainerControllerResolver;
use Symfony\Component\HttpKernel\EventListener\RouterListener;
use Symfony\Component\HttpKernel\HttpKernel;
use Symfony\Component\Routing\Matcher\UrlMatcher;
use Symfony\Component\Routing\RequestContext;
use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Protocols\GatewayAction;
use Webong\Gateway\Protocols\GatewayDecision;
use Webong\Gateway\Protocols\GatewayEvent;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\Response;
use Webong\Gateway\Protocols\RoutePlan;
use Webong\Gateway\Symfony\GatewayBundle;

final class PlanningKernelTest extends TestCase
{
    private function kernel(): HttpKernel
    {
        $container = new ContainerBuilder();
        (new GatewayBundle())->getContainerExtension()->load([['internal_token' => 'symfony-secret']], $container);
        $container->register(RoutePlanner::class, FixturePlanner::class);
        $container->register(ProtocolPlanner::class, FixturePlanner::class);
        $container->compile();
        $routes = require dirname(__DIR__, 2).'/pkg/planner/config/symfony-routes.php';
        $requests = new RequestStack();
        $events = new EventDispatcher();
        $events->addSubscriber(new RouterListener(new UrlMatcher($routes, new RequestContext()), $requests));

        return new HttpKernel($events, new ContainerControllerResolver($container), $requests, new ArgumentResolver());
    }

    public function testPlansThroughSymfonyWithoutBootingLaravel(): void
    {
        $request = Request::create('/_internal/gateway/plan', 'POST', server: [
            'CONTENT_TYPE' => 'application/json', 'HTTP_X_GATEWAY_INTERNAL' => 'symfony-secret',
        ], content: json_encode(['delivery_id' => 'one', 'method' => 'POST', 'path' => '/test', 'body' => base64_encode('payload')]));
        $response = $this->kernel()->handle($request);
        self::assertSame(200, $response->getStatusCode());
        $plan = json_decode($response->getContent(), true);
        self::assertSame('respond', $plan['action']);
        self::assertSame(base64_encode('payload'), $plan['immediate_response']['body']);
    }

    public function testHidesUnauthorizedRequestsAndRejectsMalformedPayloads(): void
    {
        $kernel = $this->kernel();
        foreach ([['', '{}', 404], ['wrong', '{}', 404], ['symfony-secret', '{', 422], ['symfony-secret', '[]', 422], ['symfony-secret', '{}', 422]] as [$token, $body, $status]) {
            $request = Request::create('/_internal/gateway/plan', 'POST', server: ['HTTP_X_GATEWAY_INTERNAL' => $token], content: $body);
            self::assertSame($status, $kernel->handle($request)->getStatusCode());
        }
    }

    public function testPlansProtocolEvents(): void
    {
        $request = Request::create('/_internal/gateway/event', 'POST', server: ['HTTP_X_GATEWAY_INTERNAL' => 'symfony-secret'], content: json_encode([
            'id' => 'dns-one', 'protocol' => 'dns', 'kind' => 'query', 'route' => '/dns',
        ]));
        $response = $this->kernel()->handle($request);
        self::assertSame(200, $response->getStatusCode());
        self::assertSame('dns', json_decode($response->getContent(), true)['protocol']);
    }

    public function testRejectsInvalidBundleConfiguration(): void
    {
        $this->expectException(\Symfony\Component\Config\Definition\Exception\InvalidConfigurationException::class);
        (new GatewayBundle())->getContainerExtension()->load([['max_body_bytes' => 0]], new ContainerBuilder());
    }

    public function testRequiresApplicationOwnedPlanningServices(): void
    {
        $container = new ContainerBuilder();
        (new GatewayBundle())->getContainerExtension()->load([['internal_token' => 'secret']], $container);
        $this->expectException(\Symfony\Component\DependencyInjection\Exception\ServiceNotFoundException::class);
        $container->compile();
    }
}

final class FixturePlanner implements RoutePlanner, ProtocolPlanner
{
    public function plan(IngressRequest $request): RoutePlan
    {
        return RoutePlan::respond(new Response(202, [], $request->body));
    }

    public function planEvent(GatewayEvent $event): GatewayDecision
    {
        return new GatewayDecision($event->protocol, GatewayAction::RESPOND);
    }
}
