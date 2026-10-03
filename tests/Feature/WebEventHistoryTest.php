<?php

declare(strict_types=1);

use Webong\Gateway\Automation\CapabilityAwareRoutePlanner;
use Webong\Gateway\Events\Models\GatewayEndpointEvent;
use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Protocol\EventKind;
use Webong\Gateway\Protocol\GatewayEvent;
use Webong\Gateway\Protocol\IngressRequest;
use Webong\Gateway\Protocol\Protocol;
use Webong\Gateway\Tests\Support\TestPathResolver;
use Webong\WebProxy\EndpointDefinition;
use Webong\WebProxy\EnsureEndpoint;

beforeEach(function (): void {
    TestPathResolver::$endpointKey = 'relay-endpoint';
    TestPathResolver::$resolutions = 0;
});

/** @param array<string, mixed> $metadata */
function webHistoryEndpoint(array $metadata = []): string
{
    $endpoint = app(EnsureEndpoint::class)->handle(new EndpointDefinition(
        client: 'relay-test',
        externalId: 'history-endpoint',
        signingSecret: null,
        verificationToken: null,
        endpointKey: 'history-endpoint',
        credentialOwnerId: 'history-owner',
        managed: false,
        metadata: $metadata,
    ));
    TestPathResolver::$endpointKey = $endpoint->record->endpoint_key;

    return $endpoint->record->endpoint_key;
}

function webHistoryRequest(string $receipt, string $body = 'private body'): IngressRequest
{
    return new IngressRequest(
        deliveryId: 'stable-delivery',
        method: 'POST',
        host: 'relay.example.test',
        path: '/provider/events/app-123',
        rawQuery: 'access_token=secret',
        headers: ['Authorization' => ['Bearer secret'], 'Cookie' => ['session=secret'], 'Content-Type' => ['text/plain']],
        body: $body,
        attributes: ['ingress_id' => $receipt, 'remote_addr' => '192.0.2.10:45555'],
    );
}

it('records repeated identical Web ingress as separate redacted events on the shared endpoint', function (): void {
    config()->set('gateway.events.enabled', true);
    $endpointKey = webHistoryEndpoint();
    $planner = app(CapabilityAwareRoutePlanner::class);

    expect($planner->plan(webHistoryRequest('receipt-1'))->immediateResponse?->statusCode)->toBe(204)
        ->and($planner->plan(webHistoryRequest('receipt-2'))->immediateResponse?->statusCode)->toBe(204)
        ->and(GatewayEndpointEvent::query()->count())->toBe(2);
    $planner->plan(webHistoryRequest('receipt-1'));
    expect(GatewayEndpointEvent::query()->count())->toBe(2);

    $this->getJson('/registry/endpoints/'.$endpointKey.'/events')->assertUnauthorized();
    $this->withToken('registry-secret')->getJson('/registry/endpoints/missing/events')->assertNotFound();
    $this->withToken('registry-secret')->getJson('/registry/endpoints/'.$endpointKey.'/events?limit=0')->assertUnprocessable();
    $this->withToken('registry-secret')->getJson('/registry/endpoints/'.$endpointKey.'/events?unknown=1')->assertUnprocessable();
    $response = $this->withToken('registry-secret')->getJson('/registry/endpoints/'.$endpointKey.'/events?protocol=http&kind=request&limit=1')
        ->assertOk()
        ->assertJsonCount(1, 'data')
        ->assertJsonPath('data.0.attributes.delivery_id', 'stable-delivery')
        ->assertJsonPath('data.0.attributes.query_present', true)
        ->assertJsonPath('data.0.payload.body_bytes', strlen('private body'));
    $item = $response->json('data.0');
    expect($item['event_id'])->toBeIn(['receipt-1', 'receipt-2'])
        ->and($item['deliveries_url'])->toBe('/deliveries?event_id='.$item['event_id'])
        ->and($item['payload'])->not->toHaveKey('body_base64')
        ->and($response->getContent())->not->toContain('Bearer secret', 'session=secret', 'access_token=secret', 'private body');
    $cursor = $response->json('next_cursor');
    expect($cursor)->toBeString();
    $this->withToken('registry-secret')->getJson('/registry/endpoints/'.$endpointKey.'/events?limit=1&cursor='.rawurlencode($cursor))
        ->assertOk()->assertJsonCount(1, 'data');
});

it('captures only an opted-in bounded body and prunes old shared events', function (): void {
    config()->set('gateway.events.enabled', true);
    config()->set('gateway.events.max_body_bytes', 4);
    config()->set('gateway.events.retention_days', 7);
    $endpointKey = webHistoryEndpoint(['_gateway' => ['capture_body' => true]]);
    app(CapabilityAwareRoutePlanner::class)->plan(webHistoryRequest('captured-1', 'top-secret'));

    $response = $this->withToken('registry-secret')->getJson('/registry/endpoints/'.$endpointKey.'/events')
        ->assertOk()
        ->assertJsonPath('data.0.payload.body_base64', base64_encode('top-'))
        ->assertJsonPath('data.0.payload.truncated', true)
        ->assertJsonPath('data.0.payload.body_bytes', strlen('top-secret'));
    expect($response->getContent())->not->toContain('top-secret', 'Bearer secret');

    GatewayEndpointEvent::query()->update(['received_at' => now()->subDays(8)]);
    $this->artisan('gateway:prune-events')->assertExitCode(0);
    expect(GatewayEndpointEvent::query()->count())->toBe(0);
});

it('leaves Web history off until explicitly enabled', function (): void {
    webHistoryEndpoint();
    app(CapabilityAwareRoutePlanner::class)->plan(webHistoryRequest('off-1'));
    expect(GatewayEndpointEvent::query()->count())->toBe(0);
});

it('records WebSocket messages and Webstream connects through the same endpoint history', function (): void {
    config()->set('gateway.events.enabled', true);
    $key = webHistoryEndpoint();
    $planner = app(ProtocolPlanner::class);
    $planner->planEvent(new GatewayEvent(
        id: 'socket-message-1', protocol: Protocol::WEBSOCKET, kind: EventKind::MESSAGE,
        route: '/provider/events/app-123', sessionId: 'socket-1', method: 'GET',
        attributes: ['remote_addr' => '192.0.2.5:51000'], payload: 'socket payload',
    ));
    $planner->planEvent(new GatewayEvent(
        id: 'stream-connect-1', protocol: Protocol::WEBSTREAM, kind: EventKind::CONNECT,
        route: '/provider/events/app-123', method: 'GET', payload: '',
    ));

    $this->withToken('registry-secret')->getJson('/registry/endpoints/'.$key.'/events?protocol=websocket')
        ->assertOk()->assertJsonCount(1, 'data')
        ->assertJsonPath('data.0.kind', 'message')
        ->assertJsonPath('data.0.attributes.session_id', 'socket-1')
        ->assertJsonPath('data.0.payload.body_bytes', strlen('socket payload'));
    $this->withToken('registry-secret')->getJson('/registry/endpoints/'.$key.'/events?protocol=webstream')
        ->assertOk()->assertJsonCount(1, 'data')
        ->assertJsonPath('data.0.kind', 'connect');
});
