<?php

declare(strict_types=1);

use Webong\Gateway\Companion\ExtensionRuntimeConfigurationRegistry;
use Webong\Gateway\Events\Models\GatewayEndpointEvent;
use Webong\Gateway\Resources\ConfigurationKindRegistry;

function dnsForwarderPayload(array $overrides = []): array
{
    return [
        'name' => 'application',
        'address' => 'resolver.internal:53',
        'transport' => 'udp',
        'domains' => ['Example.COM.'],
        'client_cidrs' => ['192.0.2.0/24'],
        'timeout_ms' => 750,
        'enabled' => true,
        ...$overrides,
    ];
}

it('manages DNS forwarders and projects active runtime policy', function (): void {
    $this->postJson('/dns/forwarders', dnsForwarderPayload())->assertUnauthorized();

    $created = $this->withToken('registry-secret')
        ->postJson('/dns/forwarders', dnsForwarderPayload())
        ->assertCreated()
        ->assertJsonPath('name', 'application')
        ->assertJsonPath('mode', 'forwarded')
        ->assertJsonPath('address', 'resolver.internal:53')
        ->assertJsonPath('domains.0', 'example.com')
        ->assertJsonPath('client_cidrs.0', '192.0.2.0/24')
        ->assertJsonPath('enabled', true);

    expect(app(ConfigurationKindRegistry::class)->contains('dns.forwarder'))->toBeTrue();
    $projected = app(ExtensionRuntimeConfigurationRegistry::class)->project('webong/dns', ['address' => ':5353']);
    expect($projected['forwarders'])->toBe([[
        'name' => 'application',
        'address' => 'resolver.internal:53',
        'transport' => 'udp',
        'domains' => ['example.com'],
        'client_cidrs' => ['192.0.2.0/24'],
        'timeout_ms' => 750,
    ]]);

    $id = $created->json('id');
    $this->postJson('/_internal/gateway/observations', [])->assertNotFound();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')->postJson('/_internal/gateway/observations', [
        'version' => 'v1',
        'id' => 'forwarded-dns-query-1',
        'type' => 'dns.query',
        'occurred_at' => now()->toAtomString(),
        'attributes' => [
            'mode' => 'forwarded', 'forwarder' => 'application',
            'qname' => 'www.example.com.', 'qtype' => 'A', 'qclass' => 'IN', 'query_id' => '7',
            'source_address' => '192.0.2.20:53000', 'transport' => 'udp',
            'response_code' => '0', 'response_name' => 'NOERROR',
            'authoritative' => 'false', 'truncated' => 'false',
            'answer_count' => '1', 'answer_types' => 'A:1', 'elapsed_ms' => '3',
        ],
    ])->assertAccepted();
    $observed = GatewayEndpointEvent::query()->where('event_id', 'forwarded-dns-query-1')->sole();
    expect($observed->endpoint_id)->toBeNull()
        ->and($observed->subject_kind)->toBe('dns.forwarder')
        ->and($observed->subject_id)->toBe($id)
        ->and($observed->attributes['mode'])->toBe('forwarded');
    $this->withToken('registry-secret')->getJson('/dns/forwarders/'.$id.'/events?type=a&transport=udp')
        ->assertOk()
        ->assertJsonCount(1, 'data')
        ->assertJsonPath('data.0.mode', 'forwarded')
        ->assertJsonPath('data.0.response.name', 'NOERROR');
    $this->withToken('registry-secret')->getJson('/dns/forwarders/'.$id.'/usage')
        ->assertOk()
        ->assertJsonPath('queries', 1)
        ->assertJsonPath('failures', 0)
        ->assertJsonPath('by_response.NOERROR', 1);

    $this->withToken('registry-secret')->putJson('/dns/forwarders/'.$id, dnsForwarderPayload())
        ->assertStatus(428);
    $disabled = $this->withToken('registry-secret')
        ->withHeader('If-Match', $created->headers->get('ETag'))
        ->putJson('/dns/forwarders/'.$id, dnsForwarderPayload(['enabled' => false, 'transport' => 'tcp']))
        ->assertOk()
        ->assertJsonPath('transport', 'tcp')
        ->assertJsonPath('enabled', false);

    expect(app(ExtensionRuntimeConfigurationRegistry::class)
        ->project('webong/dns', ['address' => ':5353'])['forwarders'])->toBe([]);
    $this->withToken('registry-secret')->withHeader('If-Match', $created->headers->get('ETag'))
        ->deleteJson('/dns/forwarders/'.$id)->assertStatus(412);
    $this->withToken('registry-secret')->withHeader('If-Match', $disabled->headers->get('ETag'))
        ->deleteJson('/dns/forwarders/'.$id)->assertNoContent();
});

it('rejects open, non-canonical, and ambiguous forwarded DNS policies', function (): void {
    $this->withToken('registry-secret')->postJson('/dns/forwarders', dnsForwarderPayload())
        ->assertCreated();

    $this->withToken('registry-secret')->postJson('/dns/forwarders', dnsForwarderPayload([
        'name' => 'duplicate',
        'address' => 'other.internal:53',
    ]))->assertUnprocessable()
        ->assertJsonPath('message', 'DNS forwarder route already exists.');

    $this->withToken('registry-secret')->postJson('/dns/forwarders', dnsForwarderPayload([
        'name' => 'open',
        'domains' => ['.'],
        'client_cidrs' => ['0.0.0.0/0'],
    ]))->assertUnprocessable();

    $this->withToken('registry-secret')->postJson('/dns/forwarders', dnsForwarderPayload([
        'name' => 'noncanonical',
        'domains' => ['internal'],
        'client_cidrs' => ['198.51.100.7/24'],
    ]))->assertUnprocessable();
});
