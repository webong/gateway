<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\PathResolver;
use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Events\Models\GatewayEndpointEvent;
use Webong\Gateway\Protocol\GatewayAction;
use Webong\Gateway\Protocol\GatewayEvent;
use Webong\Gateway\Protocol\EventKind;
use Webong\Gateway\Protocol\IngressRequest;
use Webong\Gateway\Protocol\Protocol;
use Illuminate\Support\Facades\Schema;
use Webong\WebProxy\Models\WebProxyEndpoint;

function createDnsHook($test, string $externalId = 'dns-hook-1'): array
{
    return $test->withToken('registry-secret')->postJson('/dns/hooks', [
        'external_id' => $externalId,
        'name' => 'Login verification',
        'metadata' => ['workspace' => 'workspace-42'],
    ])->assertCreated()->json();
}

it('provisions a DNS hook as a first-class WebProxy endpoint', function (): void {
    $this->postJson('/dns/hooks', ['external_id' => 'unauthorized'])->assertUnauthorized();

    $hook = createDnsHook($this);

    expect($hook['token'])->toMatch('/^[a-f0-9]{32}$/')
        ->and($hook['hostname'])->toBe($hook['token'].'.dns.example.test')
        ->and($hook['query_name_template'])->toBe('{data}.'.$hook['hostname'])
        ->and($hook['subscriptions_url'])->toBe('/registry/endpoints/'.$hook['endpoint_key'].'/subscriptions')
        ->and(WebProxyEndpoint::query()->whereKey($hook['id'])->exists())->toBeTrue();

    $endpoint = WebProxyEndpoint::query()->where('endpoint_key', $hook['endpoint_key'])->sole();
    $zone = WebProxyEndpoint::query()->where('metadata->_gateway->kind', 'dns_zone')->sole();
    expect($endpoint->client)->toBe('gateway')
        ->and($endpoint->is_managed)->toBeTrue()
        ->and($endpoint->metadata['_gateway'])->toMatchArray([
            'kind' => 'dns_hook',
            'protocol' => 'dns',
            'token' => $hook['token'],
            'hostname' => $hook['hostname'],
            'metadata' => ['workspace' => 'workspace-42'],
        ])
        ->and($endpoint->metadata['_gateway']['parent_endpoint_key'])->toBe($zone->endpoint_key)
        ->and(Schema::hasTable('dns_hooks'))->toBeFalse()
        ->and(Schema::hasTable('dns_hook_events'))->toBeFalse()
        ->and(Schema::hasTable('events'))->toBeTrue();

    $again = createDnsHook($this);
    expect($again['id'])->toBe($hook['id'])
        ->and($again['token'])->toBe($hook['token'])
        ->and(WebProxyEndpoint::query()->where('metadata->_gateway->kind', 'dns_hook')->count())->toBe(1)
        ->and(WebProxyEndpoint::query()->where('metadata->_gateway->kind', 'dns_zone')->count())->toBe(1);
});

it('resolves DNS hook tokens before delegating non-DNS paths', function (): void {
    $hook = createDnsHook($this);
    $resolver = app(PathResolver::class);
    $binding = $resolver->resolve(new IngressRequest(
        deliveryId: 'dns-resolution-1',
        method: 'POST',
        host: $hook['hostname'].'.',
        path: '/'.$hook['token'],
        rawQuery: '',
        headers: [],
        body: '{}',
        scheme: 'dns',
        protocol: 'dns',
        event: 'query',
        attributes: ['endpoint' => strtoupper($hook['token'])],
    ));

    expect($binding?->endpointKey)->toBe($hook['endpoint_key'])
        ->and($binding?->scope)->toBe('dns')
        ->and($binding?->key)->toBe('query');

    $http = $resolver->resolve(new IngressRequest(
        deliveryId: 'http-resolution-1',
        method: 'POST',
        host: 'relay.example.test',
        path: '/provider/events/app-123',
        rawQuery: '',
        headers: [],
        body: '{}',
    ));
    expect($http)->not->toBeNull();
});

it('routes DNS subscriptions and exposes query history and usage', function (): void {
    $hook = createDnsHook($this);
    $this->withToken('registry-secret')->postJson(
        '/registry/endpoints/'.$hook['endpoint_key'].'/subscriptions',
        [
            'subscriber_id' => 'workspace-42',
            'subscription_id' => 'workspace-42-dns',
            'type' => 'relay',
            'webhook_group' => 'dns',
            'routing_scope' => 'dns',
            'routing_key' => 'query',
            'url' => 'https://subscriber.example.test/dns',
            'match' => [
                'version' => 'v1',
                'rules' => ['attributes.qtype' => ['required', 'in:TXT']],
            ],
        ],
    )->assertCreated();

    $event = new GatewayEvent(
        id: 'dns-history-event-1',
        protocol: Protocol::DNS,
        kind: EventKind::QUERY,
        route: '/'.$hook['token'],
        host: 'hello.'.$hook['hostname'].'.',
        attributes: [
            'endpoint' => $hook['token'],
            'qname' => 'hello.'.$hook['hostname'].'.',
            'qtype' => 'TXT',
            'qclass' => 'IN',
            'data' => 'hello',
            'source_addr' => '192.0.2.5:53000',
            'transport' => 'udp',
        ],
        payload: json_encode([
            'name' => 'hello.'.$hook['hostname'].'.',
            'type' => 'TXT',
            'class' => 'IN',
            'endpoint' => $hook['token'],
            'labels' => ['hello'],
            'data' => 'hello',
        ], JSON_THROW_ON_ERROR),
    );
    $planner = app(ProtocolPlanner::class);
    $decision = $planner->planEvent($event);

    expect($decision->action)->toBe(GatewayAction::DELIVER)
        ->and($decision->deliveries)->toHaveCount(1)
        ->and($decision->deliveries[0]->target)->toBe('https://subscriber.example.test/dns')
        ->and(GatewayEndpointEvent::query()->count())->toBe(1);

    // Go may retry planning the same event; usage remains idempotent.
    $planner->planEvent($event);
    expect(GatewayEndpointEvent::query()->count())->toBe(1);

    $this->withToken('registry-secret')
        ->getJson('/dns/hooks/'.$hook['id'].'/events?type=txt&transport=udp')
        ->assertOk()
        ->assertJsonCount(1, 'data')
        ->assertJsonPath('data.0.event_id', 'dns-history-event-1')
        ->assertJsonPath('data.0.query.data', 'hello')
        ->assertJsonPath('data.0.decision.action', 'deliver');

    $this->withToken('registry-secret')
        ->getJson('/dns/hooks/'.$hook['id'].'/usage')
        ->assertOk()
        ->assertJsonPath('queries', 1)
        ->assertJsonPath('by_type.TXT', 1)
        ->assertJsonPath('by_transport.udp', 1)
        ->assertJsonPath('daily.0.queries', 1);
});

it('can pause and delete a DNS hook without exposing its WebProxy endpoint', function (): void {
    $hook = createDnsHook($this);

    $this->withToken('registry-secret')
        ->patchJson('/dns/hooks/'.$hook['id'], ['active' => false])
        ->assertOk()
        ->assertJsonPath('active', false);

    $request = new IngressRequest(
        deliveryId: 'paused-hook',
        method: 'POST',
        host: $hook['hostname'].'.',
        path: '/'.$hook['token'],
        rawQuery: '',
        headers: [],
        body: '{}',
        scheme: 'dns',
        protocol: 'dns',
        event: 'query',
        attributes: ['endpoint' => $hook['token']],
    );
    expect(app(PathResolver::class)->resolve($request))->toBeNull();

    $this->withToken('registry-secret')
        ->deleteJson('/dns/hooks/'.$hook['id'])
        ->assertNoContent();
    $this->withToken('registry-secret')
        ->getJson('/dns/hooks/'.$hook['id'])
        ->assertNotFound();
    expect(WebProxyEndpoint::query()->where('endpoint_key', $hook['endpoint_key'])->exists())->toBeTrue();
});
