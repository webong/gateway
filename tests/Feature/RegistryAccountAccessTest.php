<?php

declare(strict_types=1);

use Webong\Proxy\Models\WebProxyEndpoint;
use Webong\Gateway\Tests\Support\TestPathResolver;

beforeEach(function (): void {
    TestPathResolver::$endpointKey = 'shared-events';
    TestPathResolver::$resolutions = 0;
});

it('lets an account manage only its subscriptions on endpoints granted by the Gateway operator', function (): void {
    $accountAResponse = $this->withToken('registry-secret')
        ->postJson('https://localhost/accounts', ['id' => 'client-a'])
        ->assertCreated();
    $accountBResponse = $this->withToken('registry-secret')
        ->postJson('https://localhost/accounts', ['id' => 'client-b'])
        ->assertCreated();

    $endpoint = $this->withToken('registry-secret')->postJson('/registry/endpoints', [
        'client' => 'relay-test',
        'external_id' => 'shared-events',
        'endpoint_key' => 'shared-events',
        'managed' => true,
    ])->assertCreated()->json('endpoint_key');
    $otherEndpoint = $this->withToken('registry-secret')->postJson('/registry/endpoints', [
        'client' => 'relay-test',
        'external_id' => 'private-events',
        'endpoint_key' => 'private-events',
        'managed' => true,
    ])->assertCreated()->json('endpoint_key');

    $this->withToken('registry-secret')
        ->putJson('/registry/endpoints/'.$endpoint.'/accounts/client-a', ['routing_scope' => 'path', 'routing_key' => '/hooks/shared-events'])
        ->assertOk();
    $this->withToken('registry-secret')
        ->putJson('/registry/endpoints/'.$endpoint.'/accounts/client-b', ['routing_scope' => 'path', 'routing_key' => '/hooks/shared-events'])
        ->assertOk();

    $subscriptionPath = '/registry/endpoints/'.$endpoint.'/subscriptions';
    $base = [
        'type' => 'relay',
        'group' => 'orders',
        'routing_scope' => 'path',
        'routing_key' => '/hooks/shared-events',
    ];

    $accountBCreate = $this->withToken($accountBResponse->json('token'))->postJson($subscriptionPath, [
        ...$base,
        'subscription_id' => 'client-b-orders',
        'url' => 'https://client-b.example.test/hooks/events',
    ])->assertCreated()->assertJsonPath('subscriber_id', 'client-b');

    $accountACreate = $this->withToken($accountAResponse->json('token'))->postJson($subscriptionPath, [
        ...$base,
        'subscription_id' => 'client-a-orders',
        'url' => 'https://client-a.example.test/hooks/events',
        'subscriber_id' => 'client-a',
    ])->assertCreated()->assertJsonPath('subscriber_id', 'client-a');

    $accountAPath = $subscriptionPath.'/'.$accountACreate->json('id');
    $this->withToken($accountAResponse->json('token'))
        ->withHeader('If-Match', $accountACreate->headers->get('ETag'))
        ->patchJson($accountAPath, [
            'url' => 'https://client-a.example.test/hooks/events-v2',
        ])->assertOk()->assertJsonPath('url', 'https://client-a.example.test/hooks/events-v2');

    $destinationID = $accountBCreate->json('id');
    $resourcePath = $subscriptionPath.'/'.$destinationID;

    $this->withToken($accountAResponse->json('token'))->getJson($resourcePath)->assertNotFound();
    $this->withToken($accountAResponse->json('token'))->patchJson($resourcePath, [
        'url' => 'https://attacker.example.test/steal',
    ])->assertNotFound();
    $this->withToken($accountAResponse->json('token'))
        ->deleteJson($resourcePath, [], ['If-Match' => $accountBCreate->headers->get('ETag')])
        ->assertNotFound();

    $listed = $this->withToken($accountAResponse->json('token'))
        ->getJson($subscriptionPath.'?routing_scope=path&routing_key=%2Fhooks%2Fshared-events')
        ->assertOk();
    expect($listed->json('data'))->toHaveCount(1)
        ->and($listed->json('data.0.subscriber_id'))->toBe('client-a');

    $this->withToken($accountAResponse->json('token'))->postJson($subscriptionPath, [
        ...$base,
        'subscription_id' => 'spoofed',
        'url' => 'https://attacker.example.test/hooks',
        'subscriber_id' => 'client-b',
    ])->assertForbidden();

    $this->withToken($accountAResponse->json('token'))->postJson($subscriptionPath, [
        ...$base,
        'subscription_id' => 'reply-not-allowed',
        'type' => 'reply',
        'url' => 'https://client-a.example.test/hooks',
    ])->assertForbidden();
    $this->withToken($accountAResponse->json('token'))->postJson($subscriptionPath, [
        ...$base,
        'subscription_id' => 'shell-not-allowed',
        'target_kind' => 'shell',
        'hook' => 'whoami',
    ])->assertForbidden();

    $this->withToken($accountAResponse->json('token'))->postJson($subscriptionPath, [
        ...$base,
        'routing_key' => '/hooks/another-route',
        'subscription_id' => 'other-route',
        'url' => 'https://client-a.example.test/hooks/other',
    ])->assertNotFound();

    $this->withToken($accountAResponse->json('token'))->postJson(
        '/registry/endpoints/'.$otherEndpoint.'/subscriptions',
        [...$base, 'subscription_id' => 'not-granted', 'url' => 'https://client-a.example.test/hooks/events'],
    )->assertNotFound();

    $this->withToken('registry-secret')
        ->deleteJson('/registry/endpoints/'.$endpoint.'/accounts/client-a', ['routing_scope' => 'path', 'routing_key' => '/hooks/shared-events'])
        ->assertNoContent();
    $this->withToken($accountAResponse->json('token'))->getJson($accountAPath)->assertNotFound();

    expect(WebProxyEndpoint::query()->where('endpoint_key', $endpoint)->exists())->toBeTrue()
        ->and($accountACreate->json('id'))->not->toBe($destinationID);
});

it('rotates an account credential and invalidates the old token', function (): void {
    $account = $this->withToken('registry-secret')
        ->postJson('https://localhost/accounts', ['id' => 'rotating-client'])
        ->assertCreated();
    $endpoint = $this->withToken('registry-secret')->postJson('/registry/endpoints', [
        'client' => 'relay-test',
        'external_id' => 'rotating-events',
        'endpoint_key' => 'rotating-events',
        'managed' => true,
    ])->assertCreated()->json('endpoint_key');

    $this->withToken('registry-secret')
        ->putJson('/registry/endpoints/'.$endpoint.'/accounts/rotating-client', ['routing_scope' => 'path', 'routing_key' => '/hooks/rotating-events'])
        ->assertOk();

    $rotated = $this->withToken('registry-secret')
        ->postJson('https://localhost/accounts/rotating-client/token/rotate')
        ->assertOk();
    expect($rotated->headers->get('Cache-Control'))->toContain('no-store');

    $subscription = [
        'subscription_id' => 'rotating-client-events',
        'group' => 'events',
        'routing_scope' => 'path',
        'routing_key' => '/hooks/rotating-events',
        'url' => 'https://rotating-client.example.test/events',
    ];
    $path = '/registry/endpoints/'.$endpoint.'/subscriptions';

    $this->withToken($account->json('token'))->postJson($path, $subscription)->assertUnauthorized();
    $this->withToken($rotated->json('token'))->postJson($path, $subscription)
        ->assertCreated()->assertJsonPath('subscriber_id', 'rotating-client');
});
