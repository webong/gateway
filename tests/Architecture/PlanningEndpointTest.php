<?php

declare(strict_types=1);

namespace Webong\Gateway\Tests\Architecture;

use PHPUnit\Framework\TestCase;
use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Planning\PlanningEndpoint;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\Response;
use Webong\Gateway\Protocols\RoutePlan;

final class PlanningEndpointTest extends TestCase
{
    public function testRejectsRequestsBeforeCallingApplicationPolicy(): void
    {
        $planner = new class implements RoutePlanner {
            public function plan(IngressRequest $request): RoutePlan
            {
                throw new \LogicException('Policy must not run for rejected input.');
            }
        };
        $endpoint = new PlanningEndpoint('secret', 128);
        foreach ([
            ['POST', '', '{}', 404], ['POST', 'wrong', '{}', 404],
            ['GET', 'secret', '{}', 405], ['POST', 'secret', str_repeat('x', 129), 413],
            ['POST', 'secret', '{', 422], ['POST', 'secret', '[]', 422],
            ['POST', 'secret', '{}', 422], ['POST', 'secret', 'null', 422],
        ] as [$method, $token, $body, $status]) {
            $result = $endpoint->plan($method, $token, $body, $planner);
            self::assertSame($status, $result->status);
            if ($status === 405) {
                self::assertSame(['Allow' => 'POST'], $result->headers);
            }
        }
        self::assertSame(404, (new PlanningEndpoint(''))->plan('POST', '', '{}', $planner)->status);
    }

    public function testRepeatedRequestsKeepTheirOwnPayload(): void
    {
        $planner = new class implements RoutePlanner {
            public function plan(IngressRequest $request): RoutePlan
            {
                return RoutePlan::respond(new Response(202, [], $request->body));
            }
        };
        $endpoint = new PlanningEndpoint('secret');
        foreach (['first', 'second', ''] as $body) {
            $result = $endpoint->plan('POST', 'secret', json_encode([
                'delivery_id' => $body ?: 'empty', 'method' => 'POST', 'path' => '/', 'body' => base64_encode($body),
            ], JSON_THROW_ON_ERROR), $planner);
            self::assertSame(200, $result->status);
            self::assertSame(base64_encode($body), $result->payload['immediate_response']['body']);
        }
    }

    public function testDoesNotMisreportPlannerBugsAsInvalidClientInput(): void
    {
        $planner = new class implements RoutePlanner {
            public function plan(IngressRequest $request): RoutePlan
            {
                throw new \InvalidArgumentException('Application policy failed');
            }
        };
        $this->expectExceptionMessage('Application policy failed');
        (new PlanningEndpoint('secret'))->plan('POST', 'secret', '{"delivery_id":"test","method":"GET","path":"/"}', $planner);
    }
}
