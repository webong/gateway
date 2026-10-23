<?php

declare(strict_types=1);

use Webong\Gateway\Contract\RoutePlanner;
use Webong\Gateway\Protocols\Delivery;
use Webong\Gateway\Protocols\EventKind;
use Webong\Gateway\Protocols\GatewayAction;
use Webong\Gateway\Protocols\GatewayEvent;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\Protocol;
use Webong\Gateway\Protocols\RoutePlan;
use Webong\Gateway\RegistryProtocolPlanner;
use Webong\Gateway\Webstream\WebstreamRoutePolicyStore;

it('manages exact Webstream upload and retry policies through Planner', function (): void {
    $input = ['host' => 'gateway.test', 'path' => '/events', 'source' => 'https://source.test/events',
        'upload' => true, 'retry' => true];
    $this->postJson('/webstream/routes', $input)->assertUnauthorized();
    $created = $this->withToken('registry-secret')->postJson('/webstream/routes', $input)
        ->assertCreated()->assertHeader('ETag', '"1"')->assertJsonPath('upload', true)->json();
    $this->withToken('registry-secret')->postJson('/webstream/routes', $input)->assertConflict();
    $this->withToken('registry-secret')->getJson('/webstream/routes')->assertOk()->assertJsonCount(1, 'data');
    $this->withToken('registry-secret')->withHeader('If-Match', '"1"')
        ->putJson('/webstream/routes/'.$created['id'], [...$input, 'enabled' => false])
        ->assertOk()->assertHeader('ETag', '"2"')->assertJsonPath('enabled', false);
    $this->withToken('registry-secret')->withHeader('If-Match', '"2"')
        ->deleteJson('/webstream/routes/'.$created['id'])->assertNoContent();
    expect(app(WebstreamRoutePolicyStore::class)->all())->toBe([]);
});

it('enforces the stored route policy immediately and isolates other routes and sources', function (): void {
    $store = app(WebstreamRoutePolicyStore::class);
    $stored = $store->create(['host' => 'gateway.test', 'path' => '/events', 'source' => 'https://source.test/events',
        'upload' => true, 'retry' => true]);
    $route = new class implements RoutePlanner {
        public string $source = 'https://source.test/events';

        public function plan(IngressRequest $request): RoutePlan
        {
            return RoutePlan::relay(reply: new Delivery(url: $this->source));
        }
    };
    $planner = new RegistryProtocolPlanner($route, webstreamRoutePolicies: $store);
    $event = static fn (string $path, bool $upload): GatewayEvent => new GatewayEvent(
        id: 'stream', protocol: Protocol::WEBSTREAM, kind: EventKind::CONNECT,
        route: $path, host: 'gateway.test', method: 'POST',
        attributes: $upload ? ['upload_mode' => 'stream'] : [],
        headers: $upload ? [] : ['Idempotency-Key' => ['request-1']], payload: $upload ? '' : 'input',
    );
    expect($planner->planEvent($event('/events', true))->action)->toBe(GatewayAction::STREAM)
        ->and($planner->planEvent($event('/events', false))->stream->attributes['retry_idempotent'])->toBe('true')
        ->and($planner->planEvent($event('/elsewhere', true))->action)->toBe(GatewayAction::REJECT);

    $route->source = 'https://other.test/events';
    expect($planner->planEvent($event('/events', true))->action)->toBe(GatewayAction::REJECT);
    $route->source = 'https://source.test/events';
    $store->replace($stored->id, ['host' => $stored->host, 'path' => $stored->path, 'source' => $stored->source,
        'upload' => true, 'retry' => true, 'enabled' => false], $stored->revision);
    expect($planner->planEvent($event('/events', true))->action)->toBe(GatewayAction::REJECT);
});
