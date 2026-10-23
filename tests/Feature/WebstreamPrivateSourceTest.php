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
use Webong\Gateway\Webstream\WebstreamPrivateSourceStore;

it('manages exact private Webstream source grants through the authenticated Planner API', function (): void {
    $input = ['origin' => 'http://events.internal:8080', 'cidrs' => ['10.42.1.0/24']];
    $this->postJson('/webstream/private-sources', $input)->assertUnauthorized();
    foreach (['127.0.0.0/8', '10.0.0.0/7', '10.42.1.1/24', '169.254.0.0/16', '::/0'] as $cidr) {
        $this->withToken('registry-secret')->postJson('/webstream/private-sources', [...$input, 'cidrs' => [$cidr]])->assertUnprocessable();
    }
    $created = $this->withToken('registry-secret')->postJson('/webstream/private-sources', $input)
        ->assertCreated()->assertHeader('ETag', '"1"')->assertJsonPath('enabled', true)->json();
    $this->withToken('registry-secret')->postJson('/webstream/private-sources', $input)->assertConflict();
    $this->withToken('registry-secret')->getJson('/webstream/private-sources')->assertOk()->assertJsonCount(1, 'data');
    $store = app(WebstreamPrivateSourceStore::class);
    expect($store->activeCidrs($input['origin']))->toBe(['10.42.1.0/24'])
        ->and($store->activeCidrs('http://events.internal:8081'))->toBe([]);

    $this->withToken('registry-secret')->putJson('/webstream/private-sources/'.$created['id'], [...$input, 'enabled' => false])
        ->assertStatus(428);
    $this->withToken('registry-secret')->withHeader('If-Match', '"1"')
        ->putJson('/webstream/private-sources/'.$created['id'], [...$input, 'enabled' => false])
        ->assertOk()->assertHeader('ETag', '"2"');
    expect($store->activeCidrs($input['origin']))->toBe([]);
    $this->withToken('registry-secret')->withHeader('If-Match', '"1"')
        ->deleteJson('/webstream/private-sources/'.$created['id'])->assertStatus(412);
    $this->withToken('registry-secret')->withHeader('If-Match', '"2"')
        ->deleteJson('/webstream/private-sources/'.$created['id'])->assertNoContent();
    expect($store->all())->toBe([]);
});

it('carries only an active exact-origin grant in a trusted Webstream decision', function (): void {
    $store = app(WebstreamPrivateSourceStore::class);
    $store->create(['origin' => 'http://events.internal:8080', 'cidrs' => ['10.42.1.0/24']]);
    $route = new class implements RoutePlanner {
        public function plan(IngressRequest $request): RoutePlan
        {
            return RoutePlan::relay(reply: new Delivery(url: 'http://events.internal:8080/events'));
        }
    };
    $planner = new RegistryProtocolPlanner($route, webstreamPrivateSources: $store);
    $event = new GatewayEvent(id: 'stream', protocol: Protocol::WEBSTREAM, kind: EventKind::CONNECT,
        route: '/events', host: 'gateway.test', method: 'GET');
    $decision = $planner->planEvent($event);
    expect($decision->action)->toBe(GatewayAction::STREAM)
        ->and($decision->stream->attributes['private_source_cidrs'])->toBe('10.42.1.0/24');

    $grant = $store->all()[0];
    $store->replace($grant->id, ['origin' => $grant->origin, 'cidrs' => ['10.42.1.0/24'], 'enabled' => false], $grant->revision);
    expect($planner->planEvent($event)->stream->attributes['private_source_cidrs'])->toBe('');
});
