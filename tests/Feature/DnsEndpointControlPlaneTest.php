<?php

declare(strict_types=1);

use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Http;
use Illuminate\Support\Facades\Schema;
use Webong\Gateway\Contracts\PathResolver;
use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Dns\DnsProvisioner;
use Webong\Gateway\Dns\DnsProvisionerRegistry;
use Webong\Gateway\Events\Models\GatewayEndpointEvent;
use Webong\Gateway\Protocols\EventKind;
use Webong\Gateway\Protocols\GatewayAction;
use Webong\Gateway\Protocols\GatewayEvent;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\Protocol;
use Webong\Proxy\Models\WebProxyEndpoint;

function createDnsEndpoint($test, string $externalId = 'dns-endpoint-1'): array
{
    if (app(\Webong\Gateway\Dns\DnsZoneCatalog::class)->all() === []) {
        $test->withToken('registry-secret')->postJson('/dns/zones', [
            'name' => 'dns.example.test', 'nameservers' => ['ns1.example.test'],
        ])->assertCreated();
    }
    return $test->withToken('registry-secret')->postJson('/dns/endpoints', [
        'external_id' => $externalId,
        'name' => 'Login verification',
        'metadata' => ['workspace' => 'workspace-42'],
    ])->assertCreated()->json();
}

it('manages exact DNS records on either a full zone or a delegated subdomain', function (): void {
    $this->putJson('/dns/zones/sosongo.com/records', ['records' => []])->assertUnauthorized();
    foreach (['sosongo.com', 'mail.sosongo.com'] as $name) {
        $created = $this->withToken('registry-secret')->postJson('/dns/zones', [
            'name' => $name, 'nameservers' => ['ns1.gateway.example', 'ns2.gateway.example'],
        ])->assertCreated();
        $zone = $created->json();
        $records = [
            ['name' => $name, 'type' => 'MX', 'value' => 'route1.mx.cloudflare.net', 'preference' => 10],
            ['name' => $name, 'type' => 'TXT', 'value' => 'v=spf1 include:_spf.mx.cloudflare.net ~all'],
            ['name' => 'cf2024-1._domainkey.'.$name, 'type' => 'TXT', 'value' => 'v=DKIM1; p=abc'],
        ];
        $updated = $this->withToken('registry-secret')->withHeader('If-Match', $created->headers->get('ETag'))
            ->putJson('/dns/zones/'.$zone['id'].'/records', ['records' => $records])
            ->assertOk()->assertJsonPath('records.0.type', 'MX');
        $projected = app(\Webong\Gateway\Companion\ExtensionRuntimeConfigurationRegistry::class)
            ->project('webong/dns', ['address' => ':5353']);
        expect(collect($projected['zones'])->firstWhere('name', $name)['records'])->toContain([
            'name' => $name, 'type' => 'MX', 'value' => 'route1.mx.cloudflare.net', 'preference' => 10,
        ]);
        $this->withToken('registry-secret')->withHeader('If-Match', $updated->headers->get('ETag'))
            ->putJson('/dns/zones/'.$zone['id'].'/records', ['records' => [
            ['name' => 'outside.example', 'type' => 'MX', 'value' => 'route1.mx.cloudflare.net', 'preference' => 10],
        ]])->assertUnprocessable();
        $current = $this->withToken('registry-secret')->getJson('/dns/zones/'.$zone['id'])
            ->assertOk()->assertJsonCount(3, 'records');
        $this->withToken('registry-secret')->withHeader('If-Match', $current->headers->get('ETag'))
            ->deleteJson('/dns/zones/'.$zone['id'])->assertConflict();
        $this->withToken('registry-secret')->withHeader('If-Match', $current->headers->get('ETag'))
            ->putJson('/dns/zones/'.$zone['id'].'/records', ['records' => []])
            ->assertOk()->assertJsonCount(0, 'records');
    }
});

it('provisions a DNS endpoint as a first-class WebProxy endpoint', function (): void {
    $this->postJson('/dns/endpoints', ['external_id' => 'unauthorized'])->assertUnauthorized();

    $hook = createDnsEndpoint($this);

    expect($hook['token'])->toMatch('/^[a-f0-9]{32}$/')
        ->and($hook['hostname'])->toBe($hook['token'].'.dns.example.test')
        ->and($hook['query_name_template'])->toBe('{data}.'.$hook['hostname'])
        ->and($hook['subscriptions_url'])->toBe('/registry/endpoints/'.$hook['endpoint_key'].'/subscriptions')
        ->and(WebProxyEndpoint::query()->whereKey($hook['id'])->exists())->toBeTrue();

    $endpoint = WebProxyEndpoint::query()->where('endpoint_key', $hook['endpoint_key'])->sole();
    expect($endpoint->client)->toBe('gateway')
        ->and($endpoint->is_managed)->toBeTrue()
        ->and($endpoint->metadata['_gateway'])->toMatchArray([
            'kind' => 'dns_endpoint',
            'protocol' => 'dns',
            'token' => $hook['token'],
            'hostname' => $hook['hostname'],
            'metadata' => ['workspace' => 'workspace-42'],
        ])
        ->and(DB::table('configurations')->where('kind', 'dns.zone')->count())->toBe(1)
        ->and(Schema::hasTable('dns_hooks'))->toBeFalse()
        ->and(Schema::hasTable('dns_hook_events'))->toBeFalse()
        ->and(Schema::hasTable('events'))->toBeTrue();

    $again = createDnsEndpoint($this);
    expect($again['id'])->toBe($hook['id'])
        ->and($again['token'])->toBe($hook['token'])
        ->and(WebProxyEndpoint::query()->where('metadata->_gateway->kind', 'dns_endpoint')->count())->toBe(1)
        ->and(DB::table('configurations')->where('kind', 'dns.zone')->count())->toBe(1);
});

it('resolves DNS endpoint tokens before delegating non-DNS paths', function (): void {
    $hook = createDnsEndpoint($this);
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
    $hook = createDnsEndpoint($this);
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
        ->and(GatewayEndpointEvent::query()->count())->toBe(0);

    $observation = [
        'version' => 'v1',
        'id' => 'dns-history-event-1',
        'type' => 'dns.query',
        'occurred_at' => now()->toAtomString(),
        'attributes' => [
            'mode' => 'provided',
            'qname' => 'hello.'.$hook['hostname'].'.',
            'qtype' => 'TXT',
            'qclass' => 'IN',
            'query_id' => '42',
            'source_address' => '192.0.2.5:53000',
            'transport' => 'udp',
            'zone' => 'dns.example.test.',
            'endpoint' => $hook['token'],
            'data' => 'hello',
            'decision_action' => 'deliver',
            'decision_status_code' => '250',
            'response_code' => '0',
            'response_name' => 'NOERROR',
            'authoritative' => 'true',
            'truncated' => 'false',
            'answer_count' => '0',
            'elapsed_ms' => '2',
        ],
    ];
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/observations', $observation)
        ->assertAccepted();

    // Spinner may retry an observation; history remains idempotent.
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/observations', $observation)
        ->assertAccepted();
    expect(GatewayEndpointEvent::query()->count())->toBe(1);

    $this->withToken('registry-secret')
        ->getJson('/dns/endpoints/'.$hook['id'].'/events?type=txt&transport=udp')
        ->assertOk()
        ->assertJsonCount(1, 'data')
        ->assertJsonPath('data.0.event_id', 'dns-history-event-1')
        ->assertJsonPath('data.0.query.data', 'hello')
        ->assertJsonPath('data.0.decision.action', 'deliver');

    $this->withToken('registry-secret')
        ->getJson('/dns/endpoints/'.$hook['id'].'/usage')
        ->assertOk()
        ->assertJsonPath('queries', 1)
        ->assertJsonPath('by_type.TXT', 1)
        ->assertJsonPath('by_transport.udp', 1)
        ->assertJsonPath('daily.0.queries', 1);
});

it('can pause and delete a DNS endpoint without exposing its WebProxy endpoint', function (): void {
    $hook = createDnsEndpoint($this);

    $current = $this->withToken('registry-secret')->getJson('/dns/endpoints/'.$hook['id'])->assertOk();

    $this->withToken('registry-secret')
        ->withHeader('If-Match', $current->headers->get('ETag'))
        ->patchJson('/dns/endpoints/'.$hook['id'], ['enabled' => false])
        ->assertOk()
        ->assertJsonPath('enabled', false);

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

    $current = $this->withToken('registry-secret')->getJson('/dns/endpoints/'.$hook['id'])->assertOk();
    $this->withToken('registry-secret')->withHeader('If-Match', $current->headers->get('ETag'))
        ->deleteJson('/dns/endpoints/'.$hook['id'])
        ->assertNoContent();
    $this->withToken('registry-secret')
        ->getJson('/dns/endpoints/'.$hook['id'])
        ->assertNotFound();
    expect(WebProxyEndpoint::query()->where('endpoint_key', $hook['endpoint_key'])->exists())->toBeTrue();
});

it('creates independent endpoints in Planner-managed zones and rejects cross-zone routing', function (): void {
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

    $this->withToken('registry-secret')->postJson('/dns/endpoints', [
        'external_id' => 'shared',
    ])->assertUnprocessable();
    $this->withToken('registry-secret')->postJson('/dns/endpoints', [
        'external_id' => 'shared', 'zone' => 'unknown.example.test',
    ])->assertUnprocessable();

    $first = $this->withToken('registry-secret')->postJson('/dns/endpoints', [
        'external_id' => 'shared', 'zone' => 'first.example.test',
    ])->assertCreated()->json();
    $second = $this->withToken('registry-secret')->postJson('/dns/endpoints', [
        'external_id' => 'shared', 'zone' => 'second.example.test',
    ])->assertCreated()->json();
    expect($first['zone'])->toBe('first.example.test')
        ->and($second['zone'])->toBe('second.example.test')
        ->and($first['id'])->not->toBe($second['id'])
        ->and($first['hostname'])->toBe($first['token'].'.first.example.test')
        ->and($second['hostname'])->toBe($second['token'].'.second.example.test')
        ->and(DB::table('configurations')->where('kind', 'dns.zone')->count())->toBe(2);

    $this->withToken('registry-secret')->getJson('/dns/endpoints?zone=second.example.test')->assertOk()
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
        ->assertJsonPath('provisioner', 'manual')
        ->json();

    $projected = app(\Webong\Gateway\Companion\ExtensionRuntimeConfigurationRegistry::class)
        ->project('webong/dns', ['address' => ':5353']);
    expect($projected['zones'][0]['name'])->toBe('hooks.customer.test')
        ->and($projected['zones'][0]['nameservers'][0])->toBe('ns1.gateway.test');

    $hook = $this->withToken('registry-secret')->postJson('/dns/endpoints', [
        'external_id' => 'dynamic-hook',
        'zone' => 'hooks.customer.test',
    ])->assertCreated()->json();
    expect($hook['hostname'])->toBe($hook['token'].'.hooks.customer.test');

    $zoneCurrent = $this->withToken('registry-secret')->getJson('/dns/zones/'.$zone['id'])->assertOk();
    $this->withToken('registry-secret')->withHeader('If-Match', $zoneCurrent->headers->get('ETag'))->deleteJson('/dns/zones/'.$zone['id'])
        ->assertConflict();
    $endpointCurrent = $this->withToken('registry-secret')->getJson('/dns/endpoints/'.$hook['id'])->assertOk();
    $this->withToken('registry-secret')->withHeader('If-Match', $endpointCurrent->headers->get('ETag'))->deleteJson('/dns/endpoints/'.$hook['id'])->assertNoContent();
    $zoneCurrent = $this->withToken('registry-secret')->getJson('/dns/zones/'.$zone['id'])->assertOk();
    $this->withToken('registry-secret')->withHeader('If-Match', $zoneCurrent->headers->get('ETag'))->deleteJson('/dns/zones/'.$zone['id'])->assertNoContent();
    $this->withToken('registry-secret')->getJson('/dns/zones/'.$zone['id'])->assertNotFound();
});

it('exposes managed-zone query history and usage through the same observation interface', function (): void {
    $zone = $this->withToken('registry-secret')->postJson('/dns/zones', [
        'name' => 'managed.example.test',
        'nameservers' => ['ns1.gateway.test'],
    ])->assertCreated()->json();

    $this->withHeader('X-Gateway-Internal', 'internal-secret')->postJson('/_internal/gateway/observations', [
        'version' => 'v1',
        'id' => 'managed-dns-query-1',
        'type' => 'dns.query',
        'occurred_at' => now()->toAtomString(),
        'attributes' => [
            'mode' => 'managed',
            'zone' => 'managed.example.test.',
            'qname' => 'managed.example.test.',
            'qtype' => 'MX',
            'qclass' => 'IN',
            'query_id' => '44',
            'source_address' => '192.0.2.5:53000',
            'transport' => 'udp',
            'response_code' => '0',
            'response_name' => 'NOERROR',
            'authoritative' => 'true',
            'truncated' => 'false',
            'answer_count' => '1',
            'answer_types' => 'MX:1',
            'elapsed_ms' => '1',
        ],
    ])->assertAccepted();

    $this->withToken('registry-secret')->getJson('/dns/zones/'.$zone['id'].'/events?type=mx&transport=udp')
        ->assertOk()
        ->assertJsonCount(1, 'data')
        ->assertJsonPath('data.0.mode', 'managed')
        ->assertJsonPath('data.0.response.answer_types', 'MX:1');
    $this->withToken('registry-secret')->getJson('/dns/zones/'.$zone['id'].'/usage')
        ->assertOk()
        ->assertJsonPath('subject.kind', 'dns.zone')
        ->assertJsonPath('queries', 1)
        ->assertJsonPath('by_type.MX', 1);
    $this->withToken('registry-secret')->getJson('/dns/zones/'.$zone['id'].'/events?transport=http')
        ->assertUnprocessable();
});

it('automates Cloudflare subdomain delegation and removes only provider-created records', function (): void {
    config()->set('gateway.dns.provisioners.cloudflare.token', 'cloudflare-secret');
    config()->set('gateway.dns.provisioners.cloudflare.base_url', 'https://cloudflare.test/client/v4');
    $recordLookups = 0;
    Http::fake([
        'https://cloudflare.test/client/v4/zones?*' => Http::response([
            'success' => true, 'result' => [['id' => 'parent-zone', 'name' => 'customer.test']],
        ]),
        'https://cloudflare.test/client/v4/zones/parent-zone/dns_records?*' => function () use (&$recordLookups) {
            $recordLookups++;
            $record = match ($recordLookups) {
                3 => [['id' => 'ns-record-1']],
                4 => [['id' => 'ns-record-2']],
                default => [],
            };

            return Http::response(['success' => true, 'result' => $record]);
        },
        'https://cloudflare.test/client/v4/zones/parent-zone/dns_records' => Http::sequence()
            ->push(['success' => true, 'result' => ['id' => 'ns-record-1']])
            ->push(['success' => true, 'result' => ['id' => 'ns-record-2']]),
        'https://cloudflare.test/client/v4/zones/parent-zone/dns_records/*' => Http::response([
            'success' => true, 'result' => [],
        ]),
    ]);

    $zone = $this->withToken('registry-secret')->postJson('/dns/zones', [
        'name' => 'hooks.customer.test',
        'provisioner' => 'cloudflare',
        'nameservers' => ['ns1.gateway.test', 'ns2.gateway.test'],
        'ttl' => 300,
    ])->assertCreated()
        ->assertJsonPath('status', 'active')
        ->assertJsonPath('provisioner', 'cloudflare');

    Http::assertSentCount(5);
    $reconciled = $this->withToken('registry-secret')->withHeader('If-Match', $zone->headers->get('ETag'))
        ->postJson('/dns/zones/'.$zone->json('id').'/provision')->assertOk();
    Http::assertSentCount(8);
    $this->withToken('registry-secret')->withHeader('If-Match', $reconciled->headers->get('ETag'))
        ->deleteJson('/dns/zones/'.$zone->json('id'))->assertNoContent();
    Http::assertSentCount(10);
});

it('persists provisioner failures and retries provisioning without recreating the zone', function (): void {
    $provisioner = new class implements DnsProvisioner
    {
        public bool $fail = true;

        public function provision(array $zone, array $options = [], array $state = []): array
        {
            if ($this->fail) {
                throw new RuntimeException('provisioner unavailable');
            }

            return ['operation_id' => 'provisioner-operation-1'];
        }

        public function deprovision(array $zone, array $state = []): void {}
    };
    app(DnsProvisionerRegistry::class)->register('test', $provisioner);

    $failed = $this->withToken('registry-secret')->postJson('/dns/zones', [
        'name' => 'retry.example.test',
        'provisioner' => 'test',
        'nameservers' => ['ns1.gateway.test'],
    ])->assertStatus(502)
        ->assertJsonPath('data.status', 'error')
        ->assertJsonPath('data.enabled', false)
        ->json('data');

    $provisioner->fail = false;
    $this->withToken('registry-secret')
        ->withHeader('If-Match', '"'.$failed['revision'].'"')
        ->postJson('/dns/zones/'.$failed['id'].'/provision')
        ->assertOk()
        ->assertJsonPath('status', 'active')
        ->assertJsonPath('enabled', true);
    expect(DB::table('configurations')->where('kind', 'dns.zone')->count())->toBe(1);
});

it('reconciles provisioning with the provisioner state from the previous revision', function (): void {
    $provisioner = new class implements DnsProvisioner
    {
        /** @var list<array<string, mixed>> */
        public array $states = [];

        public function provision(array $zone, array $options = [], array $state = []): array
        {
            $this->states[] = $state;

            return ['operation_id' => 'stable-operation'];
        }

        public function deprovision(array $zone, array $state = []): void {}
    };
    app(DnsProvisionerRegistry::class)->register('stateful', $provisioner);

    $created = $this->withToken('registry-secret')->postJson('/dns/zones', [
        'name' => 'stateful.example.test',
        'provisioner' => 'stateful',
        'nameservers' => ['ns1.gateway.test'],
    ])->assertCreated();

    $this->withToken('registry-secret')
        ->withHeader('If-Match', $created->headers->get('ETag'))
        ->postJson('/dns/zones/'.$created->json('id').'/provision')
        ->assertOk();

    expect($provisioner->states)->toBe([
        [],
        ['operation_id' => 'stable-operation'],
    ]);
});
