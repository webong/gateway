<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Protocol\IngressRequest;
use Webong\Gateway\Protocol\RoutePlan;
use Webong\Gateway\WebhookScript\PrimiRoutePlanner;

it('decorates the configured planner only when the extension provider is installed', function (): void {
    $planner = app(RoutePlanner::class);

    expect($planner)->toBeInstanceOf(PrimiRoutePlanner::class);

    $plan = $planner->plan(new IngressRequest(
        deliveryId: 'delivery-1',
        method: 'POST',
        host: 'hooks.example.test',
        path: '/incoming',
        rawQuery: '',
        headers: [],
        body: 'payload',
    ));

    expect($plan->action)->toBe(RoutePlan::RELAY)
        ->and($plan->automation)->toBeNull()
        ->and($plan->relays)->toHaveCount(1)
        ->and($plan->relays[0]->url)->toBe('https://subscriber.example.test/hook')
        ->and($plan->immediateResponse?->statusCode)->toBe(202)
        ->and($plan->immediateResponse?->body)->toBe('scripted')
        ->and($plan->metadata)->toBe(['planner' => 'fixture']);
});
