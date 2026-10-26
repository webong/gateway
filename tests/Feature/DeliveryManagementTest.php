<?php

declare(strict_types=1);

use Illuminate\Http\Client\ConnectionException;
use Illuminate\Http\Client\Request;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Http;

const DELIVERY_ID = '0123456789abcdef0123456789abcdef';

beforeEach(function (): void {
    config()->set('gateway.delivery_admin_url', 'http://spinner.internal:5052');
});

it('authenticates delivery management before contacting Spinner', function (): void {
    Http::fake();

    $this->getJson('/deliveries')->assertUnauthorized();
    $this->getJson('/deliveries/'.DELIVERY_ID)->assertUnauthorized();
    $this->postJson('/deliveries/'.DELIVERY_ID.'/retry')->assertUnauthorized();

    Http::assertNothingSent();
});

it('lists and shows redacted delivery statuses through the private Spinner API', function (): void {
    $status = [
        'id' => DELIVERY_ID,
        'state' => 'failed',
        'protocol' => 'http',
        'adapter' => 'http',
        'attempts' => 2,
        'expected_status' => 204,
        'response_status' => 200,
        'body_assertion' => 'mismatched',
        'follow_up_id' => 'fedcba9876543210fedcba9876543210',
        'target' => 'https://secret.example.test/?token=private',
        'payload' => 'secret-body',
    ];
    Http::fake([
        'http://spinner.internal:5052/deliveries?*' => Http::response(['data' => [$status]]),
        'http://spinner.internal:5052/deliveries/'.DELIVERY_ID => Http::response($status),
    ]);

    $listed = $this->withToken('registry-secret')->getJson('/deliveries?state=failed&limit=10')->assertOk();
    $listed->assertJsonPath('data.0.id', DELIVERY_ID)
        ->assertJsonPath('data.0.expected_status', 204)
        ->assertJsonPath('data.0.response_status', 200)
        ->assertJsonPath('data.0.body_assertion', 'mismatched')
        ->assertJsonPath('data.0.follow_up_id', 'fedcba9876543210fedcba9876543210')
        ->assertJsonMissingPath('data.0.target')
        ->assertJsonMissingPath('data.0.payload');
    $shown = $this->withToken('registry-secret')->getJson('/deliveries/'.DELIVERY_ID)->assertOk();
    $shown->assertJsonPath('id', DELIVERY_ID)
        ->assertJsonPath('expected_status', 204)
        ->assertJsonPath('response_status', 200)
        ->assertJsonPath('body_assertion', 'mismatched')
        ->assertJsonPath('follow_up_id', 'fedcba9876543210fedcba9876543210')
        ->assertJsonMissingPath('target')
        ->assertJsonMissingPath('payload');

    Http::assertSentCount(2);
    Http::assertSent(fn (Request $request): bool => $request->url() === 'http://spinner.internal:5052/deliveries?state=failed&limit=10'
        && $request->hasHeader('Authorization', 'Bearer internal-secret'));
});

it('finds deliveries for an ingress event without exposing payloads', function (): void {
    $eventId = 'abcdef0123456789abcdef0123456789';
    Http::fake(['http://spinner.internal:5052/deliveries?*' => Http::response(['data' => [[
        'id' => DELIVERY_ID, 'event_id' => $eventId, 'state' => 'delivered', 'attempts' => 1,
        'payload' => 'secret-body', 'target' => 'https://secret.example.test',
    ]]])]);

    $this->withToken('registry-secret')->getJson('/deliveries?event_id='.$eventId)->assertOk()
        ->assertJsonPath('data.0.event_id', $eventId)
        ->assertJsonPath('data.0.state', 'delivered')
        ->assertJsonMissingPath('data.0.payload')
        ->assertJsonMissingPath('data.0.target');
    Http::assertSent(fn (Request $request): bool => $request->url() === 'http://spinner.internal:5052/deliveries?event_id='.$eventId.'&limit=50');

    $this->withToken('registry-secret')->getJson('/deliveries?event_id=bad/id')->assertUnprocessable();
    $this->withToken('registry-secret')->getJson('/deliveries?event_id='.$eventId.'&state=failed')->assertUnprocessable();
});

it('keeps ambiguous SMTP acceptance visible without exposing the message', function (): void {
    Http::fake(['http://spinner.internal:5052/deliveries/'.DELIVERY_ID => Http::response([
        'id' => DELIVERY_ID, 'state' => 'failed', 'attempts' => 1,
        'body_assertion' => 'acceptance_unknown', 'payload' => 'secret-body',
    ])]);
    $this->withToken('registry-secret')->getJson('/deliveries/'.DELIVERY_ID)->assertOk()
        ->assertJsonPath('body_assertion', 'acceptance_unknown')->assertJsonMissingPath('payload');
});

it('retries only a valid delivery ID and maps missing or pending states', function (): void {
    Http::fake([
        'http://spinner.internal:5052/deliveries/'.DELIVERY_ID.'/retry' => Http::sequence()
            ->push(['id' => DELIVERY_ID, 'state' => 'queued', 'attempts' => 0], 202)
            ->push(['message' => 'Delivery is already pending.'], 409),
    ]);

    $this->withToken('registry-secret')->post('/deliveries/'.DELIVERY_ID.'/retry')
        ->assertAccepted()->assertJsonPath('state', 'queued');
    $this->withToken('registry-secret')->post('/deliveries/'.DELIVERY_ID.'/retry')
        ->assertStatus(409);
    $this->withToken('registry-secret')->post('/deliveries/not-an-id/retry')
        ->assertNotFound();
    Http::assertSentCount(2);
});

it('fails closed for invalid filters, missing configuration, and unsafe Spinner responses', function (): void {
    Http::fake(['http://spinner.internal:5052/*' => Http::response(['data' => [[
        'id' => 'invalid', 'state' => 'failed', 'attempts' => 1,
    ]]])]);

    $this->withToken('registry-secret')->getJson('/deliveries?state=unknown')->assertUnprocessable();
    $this->withToken('registry-secret')->getJson('/deliveries?limit=1000')->assertUnprocessable();
    Http::assertNothingSent();

    $this->withToken('registry-secret')->getJson('/deliveries')->assertServiceUnavailable();
    config()->set('gateway.delivery_admin_url', '');
    $this->withToken('registry-secret')->getJson('/deliveries')->assertServiceUnavailable();
});

it('fails closed for an invalid failure notice reference from Spinner', function (): void {
    Http::fake(['http://spinner.internal:5052/deliveries?*' => Http::response(['data' => [[
        'id' => DELIVERY_ID,
        'state' => 'failed',
        'attempts' => 1,
        'follow_up_id' => '../unsafe',
    ]]])]);

    $this->withToken('registry-secret')->getJson('/deliveries')->assertServiceUnavailable();
});

it('aggregates recent statuses and retries on the node that owns a delivery', function (): void {
    $otherId = 'fedcba9876543210fedcba9876543210';
    config()->set('gateway.delivery_admin_nodes', [
        'north' => 'http://north.internal:5052',
        'south' => 'http://south.internal:5052',
    ]);
    Http::fake([
        'http://north.internal:5052/deliveries?*' => Http::response(['data' => [[
            'id' => $otherId, 'state' => 'failed', 'attempts' => 1, 'updated_at' => '2026-09-28T12:00:00Z',
        ]]]),
        'http://south.internal:5052/deliveries?*' => Http::response(['data' => [[
            'id' => DELIVERY_ID, 'state' => 'failed', 'attempts' => 2, 'updated_at' => '2026-09-28T12:00:00.100000001Z',
        ]]]),
        'http://north.internal:5052/deliveries/'.DELIVERY_ID => Http::response(['message' => 'Delivery not found.'], 404),
        'http://south.internal:5052/deliveries/'.DELIVERY_ID => Http::response([
            'id' => DELIVERY_ID, 'state' => 'failed', 'attempts' => 2,
        ]),
        'http://south.internal:5052/deliveries/'.DELIVERY_ID.'/retry' => Http::response([
            'id' => DELIVERY_ID, 'state' => 'queued', 'attempts' => 2,
        ], 202),
    ]);

    $this->withToken('registry-secret')->getJson('/deliveries?limit=1')->assertOk()
        ->assertJsonCount(1, 'data')
        ->assertJsonPath('data.0.id', DELIVERY_ID)
        ->assertJsonPath('data.0.node', 'south');
    $this->withToken('registry-secret')->getJson('/deliveries/'.DELIVERY_ID)->assertOk()
        ->assertJsonPath('node', 'south');
    $this->withToken('registry-secret')->post('/deliveries/'.DELIVERY_ID.'/retry')->assertAccepted()
        ->assertJsonPath('node', 'south');

    Http::assertSent(fn (Request $request): bool => $request->method() === 'POST'
        && $request->url() === 'http://south.internal:5052/deliveries/'.DELIVERY_ID.'/retry');
    Http::assertNotSent(fn (Request $request): bool => $request->method() === 'POST'
        && str_contains($request->url(), 'north.internal'));
});

it('does not report an incomplete multi-node view or call untrusted admin origins', function (): void {
    config()->set('gateway.delivery_admin_nodes', json_encode([
        'north' => 'http://north.internal:5052',
        'south' => 'http://south.internal:5052',
    ], JSON_THROW_ON_ERROR));
    Http::fake([
        'http://north.internal:5052/deliveries?*' => Http::response(['data' => []]),
        'http://south.internal:5052/deliveries?*' => Http::response(['message' => 'Unavailable.'], 503),
    ]);
    $this->withToken('registry-secret')->getJson('/deliveries')->assertServiceUnavailable();

    config()->set('gateway.delivery_admin_nodes', ['unsafe' => 'http://name:password@public.example.test/hidden']);
    Http::fake();
    $this->withToken('registry-secret')->getJson('/deliveries')->assertServiceUnavailable();
    Http::assertNothingSent();
});

it('does not report an absent delivery when a configured node could not be checked', function (): void {
    config()->set('gateway.delivery_admin_nodes', [
        'north' => 'http://north.internal:5052',
        'south' => 'http://south.internal:5052',
    ]);
    Http::fake([
        'http://north.internal:5052/deliveries/'.DELIVERY_ID => Http::response(['message' => 'Delivery not found.'], 404),
        'http://south.internal:5052/deliveries/'.DELIVERY_ID => Http::response(['message' => 'Unavailable.'], 503),
    ]);

    $this->withToken('registry-secret')->getJson('/deliveries/'.DELIVERY_ID)->assertServiceUnavailable();
    $this->withToken('registry-secret')->post('/deliveries/'.DELIVERY_ID.'/retry')->assertServiceUnavailable();
    Http::assertNotSent(fn (Request $request): bool => $request->method() === 'POST');
});

it('fails over shared-queue delivery reads when one private admin origin is down', function (): void {
    config()->set('gateway.delivery_admin_shared_urls', json_encode([
        'http://north.internal:5052', 'http://south.internal:5052',
    ], JSON_THROW_ON_ERROR));
    $attempted = [];
    Http::fake(function (Request $request) use (&$attempted) {
        $attempted[] = parse_url($request->url(), PHP_URL_HOST);
        if (str_contains($request->url(), 'north.internal')) {
            throw new ConnectionException('north is down');
        }

        return str_contains($request->url(), '?')
            ? Http::response(['data' => [['id' => DELIVERY_ID, 'state' => 'failed', 'attempts' => 1]]])
            : Http::response(['id' => DELIVERY_ID, 'state' => 'failed', 'attempts' => 1]);
    });

    $this->withToken('registry-secret')->getJson('/deliveries')->assertOk()
        ->assertJsonPath('data.0.id', DELIVERY_ID)
        ->assertJsonPath('data.0.node', 'shared');
    $this->withToken('registry-secret')->getJson('/deliveries/'.DELIVERY_ID)->assertOk()
        ->assertJsonPath('id', DELIVERY_ID)
        ->assertJsonPath('node', 'shared');
    expect($attempted)->toBe(['north.internal', 'south.internal', 'north.internal', 'south.internal']);
    Http::assertSentCount(2);
});

it('selects a live shared-queue admin before one retry POST', function (): void {
    config()->set('gateway.delivery_admin_shared_urls', [
        'http://north.internal:5052', 'http://south.internal:5052',
    ]);
    Http::fake(function (Request $request) {
        if (str_contains($request->url(), 'north.internal')) {
            throw new ConnectionException('north is down');
        }
        if ($request->method() === 'POST') {
            return Http::response(['id' => DELIVERY_ID, 'state' => 'queued', 'attempts' => 1], 202);
        }

        return Http::response(['id' => DELIVERY_ID, 'state' => 'failed', 'attempts' => 1]);
    });
    $this->withToken('registry-secret')->post('/deliveries/'.DELIVERY_ID.'/retry')->assertAccepted()
        ->assertJsonPath('node', 'shared');
    Http::assertSent(fn (Request $request): bool => $request->method() === 'POST'
        && str_contains($request->url(), 'south.internal'));
    Http::assertNotSent(fn (Request $request): bool => $request->method() === 'POST'
        && str_contains($request->url(), 'north.internal'));
});

it('does not replay an ambiguous shared-queue retry POST on another origin', function (): void {
    config()->set('gateway.delivery_admin_shared_urls', [
        'http://north.internal:5052', 'http://south.internal:5052',
    ]);
    Http::fake(function (Request $request) {
        if ($request->method() === 'POST') {
            throw new ConnectionException('response lost after retry');
        }

        return Http::response(['id' => DELIVERY_ID, 'state' => 'failed', 'attempts' => 1]);
    });
    $this->withToken('registry-secret')->post('/deliveries/'.DELIVERY_ID.'/retry')->assertServiceUnavailable();
    Http::assertNotSent(fn (Request $request): bool => $request->method() === 'POST'
        && str_contains($request->url(), 'south.internal'));
});

it('fails over a shared-queue admin 5xx but rejects a mismatched retry preflight', function (): void {
    config()->set('gateway.delivery_admin_shared_urls', [
        'http://north.internal:5052', 'http://south.internal:5052',
    ]);
    Http::fake(function (Request $request) {
        if (str_contains($request->url(), 'north.internal')) {
            return Http::response(['message' => 'Unavailable.'], 503);
        }

        return Http::response(['data' => [['id' => DELIVERY_ID, 'state' => 'failed', 'attempts' => 1]]]);
    });
    $this->withToken('registry-secret')->getJson('/deliveries')->assertOk()
        ->assertJsonPath('data.0.id', DELIVERY_ID);

    Http::fake(fn (Request $request) => Http::response([
        'id' => str_repeat('f', 32), 'state' => 'failed', 'attempts' => 1,
    ]));
    $this->withToken('registry-secret')->post('/deliveries/'.DELIVERY_ID.'/retry')->assertServiceUnavailable();
    Http::assertNotSent(fn (Request $request): bool => $request->method() === 'POST');
});

it('rejects unsafe or conflicting shared-queue admin configuration', function (): void {
    config()->set('gateway.delivery_admin_shared_urls', ['http://north.internal:5052', 'http://north.internal:5052/']);
    Http::fake();
    $this->withToken('registry-secret')->getJson('/deliveries')->assertServiceUnavailable();
    config()->set('gateway.delivery_admin_shared_urls', ['http://north.internal:5052', 'http://public.example.test/hidden']);
    $this->withToken('registry-secret')->getJson('/deliveries')->assertServiceUnavailable();
    config()->set('gateway.delivery_admin_shared_urls', ['http://north.internal:5052', 'http://south.internal:5052']);
    config()->set('gateway.delivery_admin_nodes', ['local' => 'http://local.internal:5052']);
    $this->withToken('registry-secret')->getJson('/deliveries')->assertServiceUnavailable();
    Http::assertNothingSent();
});

it('discovers live signed delivery admin endpoints without reusing provisioning nodes', function (): void {
    config()->set('gateway.delivery_admin_discovery_enabled', true);
    $expires = now()->addSeconds(30)->getTimestamp();
    foreach (['a' => 'http://north.internal:5052', 'b' => 'http://south.internal:5052'] as $id => $origin) {
        $id = str_repeat($id, 32);
        DB::table('delivery_admin_endpoints')->insert([
            'id' => $id, 'origin' => $origin, 'expires_unix' => $expires,
            'signature' => hash_hmac('sha256', $id."\n".$origin."\n".$expires, 'internal-secret'),
        ]);
    }
    Http::fake(function (Request $request) {
        if (str_contains($request->url(), 'north.internal')) {
            throw new ConnectionException('north is down');
        }

        return Http::response(['id' => DELIVERY_ID, 'state' => 'failed', 'attempts' => 1]);
    });

    $this->withToken('registry-secret')->getJson('/deliveries/'.DELIVERY_ID)->assertOk()
        ->assertJsonPath('node', 'shared');

    DB::table('delivery_admin_endpoints')->where('id', str_repeat('b', 32))->update(['signature' => str_repeat('0', 64)]);
    Http::fake();
    $this->withToken('registry-secret')->getJson('/deliveries/'.DELIVERY_ID)->assertServiceUnavailable();
});

it('does not discover expired endpoints or mix discovery with a static node map', function (): void {
    config()->set('gateway.delivery_admin_discovery_enabled', true);
    $id = str_repeat('a', 32);
    $origin = 'http://north.internal:5052';
    $expires = now()->subSecond()->getTimestamp();
    DB::table('delivery_admin_endpoints')->insert([
        'id' => $id, 'origin' => $origin, 'expires_unix' => $expires,
        'signature' => hash_hmac('sha256', $id."\n".$origin."\n".$expires, 'internal-secret'),
    ]);
    Http::fake();
    $this->withToken('registry-secret')->getJson('/deliveries')->assertServiceUnavailable();
    config()->set('gateway.delivery_admin_nodes', ['local' => 'http://local.internal:5052']);
    $this->withToken('registry-secret')->getJson('/deliveries')->assertServiceUnavailable();
    Http::assertNothingSent();
});
