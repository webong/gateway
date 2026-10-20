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
use Webong\Gateway\Dns\DnsProvider;
use Webong\Gateway\Dns\DnsProviderRegistry;
use Illuminate\Support\Facades\Schema;
use Illuminate\Support\Facades\Http;
use Webong\WebProxy\Models\WebProxyEndpoint;

function createDnsHook($test, string $externalId = 'dns-hook-1'): array
{
    if (app(\Webong\Gateway\Dns\DnsZoneCatalog::class)->all() === []) {
        $test->withToken('registry-secret')->postJson('/dns/zones', [
            'name' => 'dns.example.test', 'nameservers' => ['ns1.example.test'],
        ])->assertCreated();
    }
    return $test->withToken('registry-secret')->postJson('/dns/hooks', [
        'external_id' => $externalId,
        'name' => 'Login verification',
        'metadata' => ['workspace' => 'workspace-42'],
    ])->assertCreated()->json();
}

it('manages exact DNS records on either a full zone or a delegated subdomain', function (): void {
    $this->putJson('/dns/zones/sosongo.com/records', ['records' => []])->assertUnauthorized();
    foreach (['sosongo.com', 'mail.sosongo.com'] as $name) {
        $zone = $this->withToken('registry-secret')->postJson('/dns/zones', [
            'name' => $name, 'nameservers' => ['ns1.gateway.example', 'ns2.gateway.example'],
        ])->assertCreated()->json();
        $records = [
            ['name' => $name, 'type' => 'MX', 'value' => 'route1.mx.cloudflare.net', 'preference' => 10],
            ['name' => $name, 'type' => 'TXT', 'value' => 'v=spf1 include:_spf.mx.cloudflare.net ~all'],
            ['name' => 'cf2024-1._domainkey.'.$name, 'type' => 'TXT', 'value' => 'v=DKIM1; p=abc'],
        ];
        $this->withToken('registry-secret')->putJson('/dns/zones/'.$zone['id'].'/records', ['records' => $records])
            ->assertOk()->assertJsonPath('records.0.type', 'MX');
        $this->withHeader('X-Gateway-Internal', 'internal-secret')->getJson('/_internal/gateway/dns/zones')
            ->assertOk()->assertJsonFragment(['name' => $name, 'type' => 'MX', 'value' => 'route1.mx.cloudflare.net', 'preference' => 10]);
        $this->withToken('registry-secret')->putJson('/dns/zones/'.$zone['id'].'/records', ['records' => [
            ['name' => 'outside.example', 'type' => 'MX', 'value' => 'route1.mx.cloudflare.net', 'preference' => 10],
        ]])->assertUnprocessable();
        $this->withToken('registry-secret')->getJson('/dns/zones/'.$zone['id'])
            ->assertOk()->assertJsonCount(3, 'records');
        $this->withToken('registry-secret')->deleteJson('/dns/zones/'.$zone['id'])->assertConflict();
        $this->withToken('registry-secret')->putJson('/dns/zones/'.$zone['id'].'/records', ['records' => []])
            ->assertOk()->assertJsonCount(0, 'records');
    }
});

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
                'rules' => [
                    'route' => ['required', 'in:/'.$hook['token']],
                    'attributes.qtype' => ['required', 'in:TXT'],
                    'payload.data' => ['required', 'in:hello'],
                ],
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

it('creates independent hooks in Planner-managed zones and rejects cross-zone routing', function (): void {
    $this->withToken('registry-secret')->postJson('/dns/zones', [
        'name' => 'first.example.test', 'nameservers' => ['ns1.example.test'],
    ])->assertCreated();
    $this->withToken('registry-secret')->postJson('/dns/zones', [
        'name' => 'second.example.test', 'nameservers' => ['ns2.example.test'],
    ])->assertCreated();

    $this->withHeader('Authorization', '')->getJson('/dns/zones')->assertUnauthorized();
    $this->withToken('registry-secret')->getJson('/dns/zones')->assertOk()
        ->assertJsonCount(2, 'data')
        ->assertJsonPath('data.0.name', 'first.example.test');

    $this->withToken('registry-secret')->postJson('/dns/hooks', [
        'external_id' => 'shared',
    ])->assertUnprocessable();
    $this->withToken('registry-secret')->postJson('/dns/hooks', [
        'external_id' => 'shared', 'zone' => 'unknown.example.test',
    ])->assertUnprocessable();

    $first = $this->withToken('registry-secret')->postJson('/dns/hooks', [
        'external_id' => 'shared', 'zone' => 'first.example.test',
    ])->assertCreated()->json();
    $second = $this->withToken('registry-secret')->postJson('/dns/hooks', [
        'external_id' => 'shared', 'zone' => 'second.example.test',
    ])->assertCreated()->json();
    expect($first['zone'])->toBe('first.example.test')
        ->and($second['zone'])->toBe('second.example.test')
        ->and($first['id'])->not->toBe($second['id'])
        ->and($first['hostname'])->toBe($first['token'].'.first.example.test')
        ->and($second['hostname'])->toBe($second['token'].'.second.example.test')
        ->and(WebProxyEndpoint::query()->where('metadata->_gateway->kind', 'dns_zone')->count())->toBe(2);

    $this->withToken('registry-secret')->getJson('/dns/hooks?zone=second.example.test')->assertOk()
        ->assertJsonCount(1, 'data')
        ->assertJsonPath('data.0.id', $second['id']);

    $binding = app(PathResolver::class)->resolve(new IngressRequest(
        deliveryId: 'multi-zone-correct', method: 'POST', host: $first['hostname'].'.',
        path: '/'.$first['token'], rawQuery: '', headers: [], body: '{}',
        scheme: 'dns', protocol: 'dns', event: 'query',
        attributes: ['endpoint' => $first['token'], 'zone' => 'first.example.test.'],
    ));
    expect($binding?->endpointKey)->toBe($first['endpoint_key']);

    $wrongZone = app(PathResolver::class)->resolve(new IngressRequest(
        deliveryId: 'multi-zone-wrong', method: 'POST', host: $first['token'].'.second.example.test.',
        path: '/'.$first['token'], rawQuery: '', headers: [], body: '{}',
        scheme: 'dns', protocol: 'dns', event: 'query',
        attributes: ['endpoint' => $first['token'], 'zone' => 'second.example.test.'],
    ));
    expect($wrongZone)->toBeNull();

    $planner = app(ProtocolPlanner::class);
    $validEvent = new GatewayEvent(
        id: 'multi-zone-cached-valid', protocol: Protocol::DNS, kind: EventKind::QUERY,
        route: '/'.$first['token'], host: 'hello.'.$first['hostname'].'.',
        attributes: ['endpoint' => $first['token'], 'zone' => 'first.example.test.'],
    );
    $wrongEvent = new GatewayEvent(
        id: 'multi-zone-cached-wrong', protocol: Protocol::DNS, kind: EventKind::QUERY,
        route: '/'.$first['token'], host: 'hello.'.$first['token'].'.second.example.test.',
        attributes: ['endpoint' => $first['token'], 'zone' => 'second.example.test.'],
    );
    expect($planner->planEvent($validEvent)->action)->toBe(GatewayAction::RESPOND)
        ->and($planner->planEvent($wrongEvent)->action)->toBe(GatewayAction::REJECT);
});

it('provisions registry-backed zones through the API', function (): void {

    $zone = $this->withToken('registry-secret')->postJson('/dns/zones', [
        'name' => 'hooks.customer.test',
        'nameservers' => ['ns1.gateway.test', 'ns2.gateway.test'],
    ])->assertCreated()
        ->assertJsonPath('name', 'hooks.customer.test')
        ->assertJsonPath('status', 'active')
        ->assertJsonPath('provider', 'manual')
        ->json();

    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->getJson('/_internal/gateway/dns/zones')
        ->assertOk()
        ->assertJsonPath('data.0.name', 'hooks.customer.test')
        ->assertJsonPath('data.0.nameservers.0', 'ns1.gateway.test');
    $this->withHeader('X-Gateway-Internal', '')->getJson('/_internal/gateway/dns/zones')->assertNotFound();

    $hook = $this->withToken('registry-secret')->postJson('/dns/hooks', [
        'external_id' => 'dynamic-hook',
        'zone' => 'hooks.customer.test',
    ])->assertCreated()->json();
    expect($hook['hostname'])->toBe($hook['token'].'.hooks.customer.test');

    $this->withToken('registry-secret')->deleteJson('/dns/zones/'.$zone['id'])
        ->assertConflict();
    $this->withToken('registry-secret')->deleteJson('/dns/hooks/'.$hook['id'])->assertNoContent();
    $this->withToken('registry-secret')->deleteJson('/dns/zones/'.$zone['id'])->assertNoContent();
    $this->withToken('registry-secret')->getJson('/dns/zones/'.$zone['id'])->assertNotFound();
});

it('automates Cloudflare subdomain delegation and removes only provider-created records', function (): void {
    config()->set('gateway.dns.providers.cloudflare.token', 'cloudflare-secret');
    config()->set('gateway.dns.providers.cloudflare.base_url', 'https://cloudflare.test/client/v4');
    Http::fake([
        'https://cloudflare.test/client/v4/zones?*' => Http::response([
            'success' => true, 'result' => [['id' => 'parent-zone', 'name' => 'customer.test']],
        ]),
        'https://cloudflare.test/client/v4/zones/parent-zone/dns_records?*' => Http::response([
            'success' => true, 'result' => [],
        ]),
        'https://cloudflare.test/client/v4/zones/parent-zone/dns_records' => Http::sequence()
            ->push(['success' => true, 'result' => ['id' => 'ns-record-1']])
            ->push(['success' => true, 'result' => ['id' => 'ns-record-2']]),
        'https://cloudflare.test/client/v4/zones/parent-zone/dns_records/*' => Http::response([
            'success' => true, 'result' => [],
        ]),
    ]);

    $zone = $this->withToken('registry-secret')->postJson('/dns/zones', [
        'name' => 'hooks.customer.test',
        'provider' => 'cloudflare',
        'nameservers' => ['ns1.gateway.test', 'ns2.gateway.test'],
        'ttl' => 300,
    ])->assertCreated()
        ->assertJsonPath('status', 'active')
        ->assertJsonPath('provider', 'cloudflare')
        ->json();

    Http::assertSentCount(5);
    $this->withToken('registry-secret')->deleteJson('/dns/zones/'.$zone['id'])->assertNoContent();
    Http::assertSentCount(7);
});

it('persists provider failures and retries provisioning without recreating the zone', function (): void {
    $provider = new class implements DnsProvider
    {
        public bool $fail = true;

        public function provision(array $zone, array $options = []): array
        {
            if ($this->fail) {
                throw new RuntimeException('provider unavailable');
            }

            return ['operation_id' => 'provider-operation-1'];
        }

        public function deprovision(array $zone, array $state = []): void {}
    };
    app(DnsProviderRegistry::class)->register('test', $provider);

    $failed = $this->withToken('registry-secret')->postJson('/dns/zones', [
        'name' => 'retry.example.test',
        'provider' => 'test',
        'nameservers' => ['ns1.gateway.test'],
    ])->assertStatus(502)
        ->assertJsonPath('data.status', 'error')
        ->assertJsonPath('data.active', false)
        ->json('data');

    $provider->fail = false;
    $this->withToken('registry-secret')
        ->postJson('/dns/zones/'.$failed['id'].'/provision')
        ->assertOk()
        ->assertJsonPath('status', 'active')
        ->assertJsonPath('active', true);
    expect(WebProxyEndpoint::query()->where('metadata->_gateway->kind', 'dns_zone')->count())->toBe(1);
});
