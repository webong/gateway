<?php

declare(strict_types=1);

use Webong\Proxy\Endpoint;
use Webong\Proxy\EndpointDefinition;
use Webong\Proxy\EnsureEndpoint;
use Webong\Proxy\Models\WebProxyDestination;
use Webong\Proxy\Models\WebProxyEndpoint;
use Webong\Proxy\ProfileDefinition;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Contract\AutomationCapability;
use Webong\Gateway\Contract\AutomationExecutionTarget;
use Webong\Gateway\Contract\AutomationLanguageRegistry;
use Webong\Gateway\Automation\CapabilityAwareRoutePlanner;
use Webong\Gateway\Protocols\SubscriptionState;
use Webong\Gateway\RegistryRoutePlanner;
use Webong\Gateway\RegistryRouteCache;
use Webong\Gateway\SubscribeEndpoint;
use Webong\Gateway\ShellHookGrantStore;
use Webong\Gateway\SubscriptionDefinition;
use Webong\Gateway\Tests\Support\TestPathResolver;

beforeEach(function (): void {
    TestPathResolver::$endpointKey = 'relay-endpoint';
    TestPathResolver::$resolutions = 0;
});

function relayEndpoint(): Endpoint
{
    $endpoint = app(EnsureEndpoint::class)->handle(new EndpointDefinition(
        client: 'relay-test',
        externalId: 'provider-app-123',
        signingSecret: 'provider-secret',
        verificationToken: 'verification-token',
        endpointKey: 'relay-endpoint',
        credentialOwnerId: 'endpoint-owner',
        managed: false,
    ));

    TestPathResolver::$endpointKey = $endpoint->record->endpoint_key;

    return $endpoint;
}

/** @return list<object> */
function grantShellHooks(string $endpointKey, array $subscribers): array
{
    $grants = [];
    foreach ($subscribers as $subscriberId => $hooks) {
        foreach ($hooks as $hook) {
            $grants[] = app(ShellHookGrantStore::class)->create([
                'endpoint_key' => $endpointKey, 'subscriber_id' => $subscriberId, 'hook' => $hook,
            ]);
        }
    }

    return $grants;
}

function subscriptionEtag(WebProxyDestination $destination): string
{
    return SubscriptionState::etag(SubscriptionState::revision($destination->metadata ?? []));
}

it('registers endpoints through the PHP-owned registry API', function (): void {
    $response = $this->withToken('registry-secret')->postJson('/registry/endpoints', [
        'client' => 'relay-test',
        'external_id' => 'remote-provider-app-123',
        'endpoint_key' => 'remote-endpoint',
        'signing_secret' => 'provider-secret',
        'verification_token' => 'verification-token',
        'credential_owner_id' => 'endpoint-owner',
        'metadata' => ['environment' => 'test'],
    ]);

    $response->assertCreated()
        ->assertJsonPath('client', 'relay-test')
        ->assertJsonPath('external_id', 'remote-provider-app-123')
        ->assertJsonPath('managed', false)
        ->assertJsonPath('active', true)
        ->assertJsonPath('metadata.environment', 'test');

    $endpointKey = $response->json('endpoint_key');
    expect($endpointKey)->toStartWith('remote-endpoint-')
        ->and(WebProxyEndpoint::query()->where('endpoint_key', $endpointKey)->exists())->toBeTrue();
});

it('does not expose the old package-prefixed registry API', function (): void {
    $this->withToken('registry-secret')
        ->postJson('/gateway/endpoints/legacy/subscriptions', [])
        ->assertNotFound();
});

it('registers the canonical match rules through remote HTTP JSON', function (): void {
    $endpoint = relayEndpoint();

    $response = $this->withToken('registry-secret')->postJson(
        '/registry/endpoints/'.$endpoint->record->endpoint_key.'/subscriptions',
        [
            'subscriber_id' => 'workspace-42',
            'subscription_id' => 'workspace-42-messages',
            'type' => 'relay',
            'webhook_group' => 'meta',
            'routing_scope' => 'application',
            'routing_key' => 'app-123',
            'url' => 'https://subscriber.example.test/webhooks/meta',
            'match' => [
                'version' => 'v1',
                'rules' => [
                    'headers.X-Event-Type' => ['required', 'in:message.created'],
                    'body.account.id' => ['required', 'in:account-42'],
                ],
            ],
        ],
    );

    $response->assertCreated()
        ->assertHeader('ETag', SubscriptionState::etag($response->json('revision')))
        ->assertJsonPath('status', 'active');
    expect($response->json('match.rules'))->toBe([
        'headers.x-event-type' => ['required', 'in:message.created'],
        'body.account.id' => ['required', 'in:account-42'],
    ]);

    $destination = WebProxyDestination::query()->sole();
    expect($destination->metadata['_gateway_match'])->toBe([
        'version' => 'v1',
        'rules' => [
            'headers.x-event-type' => ['required', 'in:message.created'],
            'body.account.id' => ['required', 'in:account-42'],
        ],
    ]);
});

it('returns only subscriptions whose rules match to the Go route plan', function (): void {
    $endpoint = relayEndpoint();
    $subscriptions = app(SubscribeEndpoint::class);

    foreach ([
        ['matching', 'account-42'],
        ['not-matching', 'account-99'],
    ] as [$subscriber, $account]) {
        $subscriptions->handle($endpoint->record->endpoint_key, SubscriptionDefinition::matching(
            subscriberId: $subscriber,
            subscriptionId: $subscriber.'-messages',
            webhookGroup: 'meta',
            routingScope: 'application',
            routingKey: 'app-123',
            url: "https://{$subscriber}.example.test/webhooks/meta",
            rules: [
                'headers.x-event-type' => ['required', 'in:message.created'],
                'body.account.id' => ['required', "in:{$account}"],
            ],
        ));
    }

    $plan = app(RegistryRoutePlanner::class)->plan(new IngressRequest(
        deliveryId: 'delivery-1',
        method: 'POST',
        host: 'relay.example.test',
        path: '/provider/events/app-123',
        rawQuery: '',
        headers: ['X-Event-Type' => ['message.created']],
        body: '{"account":{"id":"account-42"}}',
    ));

    expect($plan->action)->toBe('relay')
        ->and($plan->relays)->toHaveCount(1)
        ->and($plan->relays[0]->subscriberId)->toBe('matching')
        ->and($plan->relays[0]->url)->toBe('https://matching.example.test/webhooks/meta');
});

it('resolves an endpoint network profile into the Go delivery contract', function (): void {
    $endpoint = relayEndpoint();
    $endpoint->registerProfile(new ProfileDefinition(
        name: 'proxy',
        configuration: [
            'proxy' => [
                'http' => 'http://proxy.example.test:8080',
                'https' => 'socks5h://proxy.example.test:1080',
                'no' => ['.internal.example.test', 'metadata.example.test'],
            ],
            'options' => [
                'connect_timeout' => 5,
                'timeout' => 15,
                'headers' => [
                    'Authorization' => 'Bearer source-secret',
                    'X-Source-Scope' => ['orders', 'events'],
                ],
            ],
        ],
    ));

    $created = $this->withToken('registry-secret')->postJson(
        '/registry/endpoints/'.$endpoint->record->endpoint_key.'/subscriptions',
        [
            'subscriber_id' => 'profiled-subscriber',
            'webhook_group' => 'meta',
            'routing_scope' => 'application',
            'routing_key' => 'app-123',
            'url' => 'https://profiled.example.test/hook',
            'profile' => 'proxy',
        ],
    );
    $created->assertCreated()
        ->assertJsonPath('profile', 'proxy')
        ->assertJsonMissingPath('metadata._gateway_profile');

    $plan = app(RegistryRoutePlanner::class)->plan(new IngressRequest(
        deliveryId: 'profiled-delivery',
        method: 'POST',
        host: 'relay.example.test',
        path: '/provider/events/app-123',
        rawQuery: '',
        headers: [],
        body: '{}',
    ));

    expect($plan->relays)->toHaveCount(1)
        ->and($plan->relays[0]->transport?->toArray())->toBe([
            'proxy' => [
                'http' => 'http://proxy.example.test:8080',
                'https' => 'socks5h://proxy.example.test:1080',
                'no_proxy' => ['.internal.example.test', 'metadata.example.test'],
            ],
            'connect_timeout_seconds' => 5.0,
            'request_timeout_seconds' => 15.0,
            'stream_headers' => [
                'Authorization' => ['Bearer source-secret'],
                'X-Source-Scope' => ['orders', 'events'],
            ],
        ]);

    $removed = $this->withToken('registry-secret')
        ->withHeader('If-Match', $created->headers->get('ETag'))
        ->patchJson(
            '/registry/endpoints/'.$endpoint->record->endpoint_key.'/subscriptions/'.$created->json('id'),
            ['profile' => null],
        );
    $removed->assertOk()->assertJsonPath('profile', null);

    expect(app(RegistryRoutePlanner::class)->plan(new IngressRequest(
        deliveryId: 'default-delivery',
        method: 'POST',
        host: 'relay.example.test',
        path: '/provider/events/app-123',
        rawQuery: '',
        headers: [],
        body: '{}',
    ))->relays[0]->transport)->toBeNull();
});

it('rejects a subscription that selects an unavailable network profile', function (): void {
    $endpoint = relayEndpoint();

    $this->withToken('registry-secret')->postJson(
        '/registry/endpoints/'.$endpoint->record->endpoint_key.'/subscriptions',
        [
            'subscriber_id' => 'profiled-subscriber',
            'webhook_group' => 'meta',
            'routing_scope' => 'application',
            'routing_key' => 'app-123',
            'url' => 'https://profiled.example.test/hook',
            'profile' => 'missing',
        ],
    )->assertUnprocessable()
        ->assertJsonPath('errors.subscription.0', 'Proxy profile [missing] is not active for endpoint ['.$endpoint->record->endpoint_key.'].');
});

it('patches subscription rules incrementally and invalidates the cached route', function (): void {
    $endpoint = relayEndpoint();
    $destination = app(SubscribeEndpoint::class)->handle(
        $endpoint->record->endpoint_key,
        SubscriptionDefinition::matching(
            subscriberId: 'incremental',
            subscriptionId: 'incremental-messages',
            webhookGroup: 'meta',
            routingScope: 'application',
            routingKey: 'app-123',
            url: 'https://incremental.example.test/webhooks/meta',
            rules: ['body.account.id' => ['required', 'string', 'in:account-42']],
        ),
    );
    $planner = app(RegistryRoutePlanner::class);
    $request = static fn (string $account, string $delivery): IngressRequest => new IngressRequest(
        deliveryId: $delivery,
        method: 'POST',
        host: 'relay.example.test',
        path: '/provider/events/app-123',
        rawQuery: '',
        headers: [],
        body: json_encode(['account' => ['id' => $account]], JSON_THROW_ON_ERROR),
    );

    // Prime the route cache with the original rule set.
    expect($planner->plan($request('account-42', 'before-patch'))->relays)->toHaveCount(1);

    $this->withToken('registry-secret')
        ->withHeader('If-Match', subscriptionEtag(WebProxyDestination::query()->findOrFail($destination->id)))
        ->patchJson(
        '/registry/endpoints/'.$endpoint->record->endpoint_key.'/subscriptions/'.$destination->id.'/match',
        [
            'version' => 'v1',
            'operations' => [
                [
                    'op' => 'remove',
                    'field' => 'body.account.id',
                    'rules' => ['in:account-42'],
                ],
                [
                    'op' => 'add',
                    'field' => 'body.account.id',
                    'rules' => ['in:account-99'],
                ],
            ],
        ],
    )->assertOk()->assertJsonPath('match.rules', [
        'body.account.id' => ['required', 'string', 'in:account-99'],
    ]);

    expect($planner->plan($request('account-42', 'old-value'))->immediateResponse?->statusCode)->toBe(204)
        ->and($planner->plan($request('account-99', 'new-value'))->relays)->toHaveCount(1);

    expect(WebProxyDestination::query()->findOrFail($destination->id)->metadata['_gateway_match']['rules'])
        ->toBe(['body.account.id' => ['required', 'string', 'in:account-99']]);
});

it('rejects unsafe incremental match rules', function (): void {
    $endpoint = relayEndpoint();
    $destination = app(SubscribeEndpoint::class)->handle(
        $endpoint->record->endpoint_key,
        SubscriptionDefinition::matching(
            subscriberId: 'incremental',
            subscriptionId: 'incremental-messages',
            webhookGroup: 'meta',
            routingScope: 'application',
            routingKey: 'app-123',
            url: 'https://incremental.example.test/webhooks/meta',
            rules: [],
        ),
    );

    $this->withToken('registry-secret')
        ->withHeader('If-Match', subscriptionEtag(WebProxyDestination::query()->findOrFail($destination->id)))
        ->patchJson(
        '/registry/endpoints/'.$endpoint->record->endpoint_key.'/subscriptions/'.$destination->id.'/match',
        [
            'version' => 'v1',
            'operations' => [[
                'op' => 'add',
                'field' => 'body.account.id',
                'rules' => ['exists:accounts,id'],
            ]],
        ],
    )->assertUnprocessable()
        ->assertJsonPath('errors.match.0', 'Match rule [exists] is not allowed.');

    $this->withToken('registry-secret')
        ->withHeader('If-Match', subscriptionEtag(WebProxyDestination::query()->findOrFail($destination->id)))
        ->patchJson(
            '/registry/endpoints/'.$endpoint->record->endpoint_key.'/subscriptions/'.$destination->id.'/match',
            [
                'version' => 'v1',
                'operations' => [[
                    'op' => 'add',
                    'field' => 'payload.size',
                    'rules' => ['max:1e999'],
                ]],
            ],
        )->assertUnprocessable()
        ->assertJsonPath('errors.match.0', 'Match-rule field [payload.size] has invalid [max] parameters.');
});

it('does not patch a destination through a different endpoint', function (): void {
    $endpoint = relayEndpoint();
    $destination = app(SubscribeEndpoint::class)->handle(
        $endpoint->record->endpoint_key,
        SubscriptionDefinition::matching(
            subscriberId: 'endpoint-owner',
            subscriptionId: 'endpoint-owner-messages',
            webhookGroup: 'meta',
            routingScope: 'application',
            routingKey: 'app-123',
            url: 'https://endpoint-owner.example.test/webhooks/meta',
            rules: [],
        ),
    );
    $other = app(EnsureEndpoint::class)->handle(new EndpointDefinition(
        client: 'relay-test',
        externalId: 'other-provider-app',
        signingSecret: 'other-secret',
        verificationToken: 'other-token',
        endpointKey: 'other-endpoint',
        credentialOwnerId: 'other-owner',
        managed: false,
    ));

    $this->withToken('registry-secret')
        ->withHeader('If-Match', subscriptionEtag(WebProxyDestination::query()->findOrFail($destination->id)))
        ->patchJson(
        '/registry/endpoints/'.$other->record->endpoint_key.'/subscriptions/'.$destination->id.'/match',
        [
            'version' => 'v1',
            'operations' => [[
                'op' => 'add',
                'field' => 'body.kind',
                'rules' => ['required'],
            ]],
        ],
    )->assertNotFound();
});

it('manages the complete subscription lifecycle with route-scoped listings and revisions', function (): void {
    $endpoint = relayEndpoint();
    $destination = app(SubscribeEndpoint::class)->handle(
        $endpoint->record->endpoint_key,
        SubscriptionDefinition::matching(
            subscriberId: 'managed-subscriber',
            subscriptionId: 'managed-subscription',
            webhookGroup: 'meta',
            routingScope: 'application',
            routingKey: 'app-123',
            url: 'https://before.example.test/webhooks/meta',
            rules: [],
            metadata: ['team' => 'platform'],
        ),
    );
    $base = '/registry/endpoints/'.$endpoint->record->endpoint_key.'/subscriptions';
    $etag = subscriptionEtag(WebProxyDestination::query()->findOrFail($destination->id));

    $this->withToken('registry-secret')
        ->getJson($base.'?routing_scope=application&routing_key=app-123')
        ->assertOk()
        ->assertJsonCount(1, 'data')
        ->assertJsonPath('data.0.id', $destination->id)
        ->assertJsonPath('data.0.status', 'active');

    $show = $this->withToken('registry-secret')->getJson($base.'/'.$destination->id);
    $show->assertOk()
        ->assertHeader('ETag', $etag)
        ->assertJsonPath('metadata.team', 'platform');

    $updated = $this->withToken('registry-secret')
        ->withHeader('If-Match', $etag)
        ->patchJson($base.'/'.$destination->id, [
            'url' => 'https://after.example.test/webhooks/meta',
            'metadata' => ['team' => null, 'environment' => 'production'],
        ]);
    $updated->assertOk()
        ->assertJsonPath('url', 'https://after.example.test/webhooks/meta')
        ->assertJsonMissingPath('metadata.team')
        ->assertJsonPath('metadata.environment', 'production');
    $updatedEtag = $updated->headers->get('ETag');
    expect($updatedEtag)->not->toBe($etag);

    $this->withToken('registry-secret')
        ->withHeader('If-Match', $etag)
        ->patchJson($base.'/'.$destination->id, ['url' => 'https://stale.example.test'])
        ->assertStatus(412)
        ->assertHeader('ETag', $updatedEtag)
        ->assertJsonPath('current_revision', $updated->json('revision'));

    $planner = app(RegistryRoutePlanner::class);
    $request = new IngressRequest(
        deliveryId: 'lifecycle-active',
        method: 'POST',
        host: 'relay.example.test',
        path: '/provider/events/app-123',
        rawQuery: '',
        headers: [],
        body: '{}',
    );
    expect($planner->plan($request)->relays[0]->url)->toBe('https://after.example.test/webhooks/meta');

    $paused = $this->withToken('registry-secret')
        ->withHeader('If-Match', $updatedEtag)
        ->patchJson($base.'/'.$destination->id.'/status', ['status' => 'paused']);
    $paused->assertOk()->assertJsonPath('status', 'paused');
    expect($planner->plan($request)->immediateResponse?->statusCode)->toBe(204);

    $active = $this->withToken('registry-secret')
        ->withHeader('If-Match', $paused->headers->get('ETag'))
        ->patchJson($base.'/'.$destination->id.'/status', ['status' => 'active']);
    $active->assertOk()->assertJsonPath('status', 'active');
    expect($planner->plan($request)->relays)->toHaveCount(1);

    $removed = $this->withToken('registry-secret')
        ->withHeader('If-Match', $active->headers->get('ETag'))
        ->deleteJson($base.'/'.$destination->id);
    $removed->assertNoContent();
    expect($planner->plan($request)->immediateResponse?->statusCode)->toBe(204);

    $this->withToken('registry-secret')
        ->getJson($base.'?routing_scope=application&routing_key=app-123')
        ->assertOk()
        ->assertJsonCount(0, 'data');
    $this->withToken('registry-secret')
        ->getJson($base.'?routing_scope=application&routing_key=app-123&include_removed=true')
        ->assertOk()
        ->assertJsonCount(1, 'data')
        ->assertJsonPath('data.0.status', 'removed');

    $reactivated = $this->withToken('registry-secret')
        ->withHeader('If-Match', $removed->headers->get('ETag'))
        ->patchJson($base.'/'.$destination->id.'/status', ['status' => 'active']);
    $reactivated->assertOk()->assertJsonPath('status', 'active');
    expect($planner->plan($request)->relays)->toHaveCount(1);
});

it('requires an If-Match revision for subscription mutations', function (): void {
    $endpoint = relayEndpoint();
    $destination = app(SubscribeEndpoint::class)->handle(
        $endpoint->record->endpoint_key,
        SubscriptionDefinition::matching(
            subscriberId: 'conditional',
            subscriptionId: 'conditional-subscription',
            webhookGroup: 'meta',
            routingScope: 'application',
            routingKey: 'app-123',
            url: 'https://conditional.example.test/webhooks/meta',
            rules: [],
        ),
    );

    $this->withToken('registry-secret')->patchJson(
        '/registry/endpoints/'.$endpoint->record->endpoint_key.'/subscriptions/'.$destination->id.'/match',
        [
            'version' => 'v1',
            'operations' => [[
                'op' => 'add',
                'field' => 'body.kind',
                'rules' => ['required'],
            ]],
        ],
    )->assertStatus(428);
});

it('does not reactivate a reply after another active reply takes the route', function (): void {
    $endpoint = relayEndpoint();
    $subscriptions = app(SubscribeEndpoint::class);
    $definition = static fn (string $subscriber): SubscriptionDefinition => SubscriptionDefinition::matching(
        subscriberId: $subscriber,
        subscriptionId: $subscriber.'-reply',
        webhookGroup: 'meta',
        routingScope: 'application',
        routingKey: 'app-123',
        url: "https://{$subscriber}.example.test/webhooks/meta",
        rules: [],
        type: 'reply',
    );
    $first = $subscriptions->handle($endpoint->record->endpoint_key, $definition('first-paused'));
    $base = '/registry/endpoints/'.$endpoint->record->endpoint_key.'/subscriptions/';

    $paused = $this->withToken('registry-secret')
        ->withHeader('If-Match', subscriptionEtag(WebProxyDestination::query()->findOrFail($first->id)))
        ->patchJson($base.$first->id.'/status', ['status' => 'paused']);
    $paused->assertOk();

    $subscriptions->handle($endpoint->record->endpoint_key, $definition('second-active'));

    $this->withToken('registry-secret')
        ->withHeader('If-Match', $paused->headers->get('ETag'))
        ->patchJson($base.$first->id.'/status', ['status' => 'active'])
        ->assertUnprocessable()
        ->assertJsonPath('errors.subscription.0', 'This route already has a synchronous reply subscription.');
});

it('caches application path bindings across payloads', function (): void {
    relayEndpoint();
    $planner = app(RegistryRoutePlanner::class);

    foreach (['account-42', 'account-99'] as $account) {
        $planner->plan(new IngressRequest(
            deliveryId: 'delivery-'.$account,
            method: 'POST',
            host: 'relay.example.test',
            path: '/provider/events/app-123',
            rawQuery: '',
            headers: [],
            body: json_encode(['account' => ['id' => $account]], JSON_THROW_ON_ERROR),
        ));
    }

    expect(TestPathResolver::$resolutions)->toBe(1);
});

it('invalidates cached subscriptions after attaching a destination', function (): void {
    $endpoint = relayEndpoint();
    $subscriptions = app(SubscribeEndpoint::class);
    $planner = app(RegistryRoutePlanner::class);

    $subscribe = static function (string $subscriber) use ($subscriptions, $endpoint): void {
        $subscriptions->handle($endpoint->record->endpoint_key, SubscriptionDefinition::matching(
            subscriberId: $subscriber,
            subscriptionId: $subscriber.'-messages',
            webhookGroup: 'meta',
            routingScope: 'application',
            routingKey: 'app-123',
            url: "https://{$subscriber}.example.test/webhooks/meta",
            rules: [],
        ));
    };
    $request = new IngressRequest(
        deliveryId: 'delivery-cache-invalidation',
        method: 'POST',
        host: 'relay.example.test',
        path: '/provider/events/app-123',
        rawQuery: '',
        headers: [],
        body: '{}',
    );

    $subscribe('first');
    expect($planner->plan($request)->relays)->toHaveCount(1);

    $subscribe('second');
    expect($planner->plan($request)->relays)->toHaveCount(2);
});

it('invalidates a cached missing endpoint after registry creation', function (): void {
    TestPathResolver::$endpointKey = 'future-endpoint';
    $planner = app(RegistryRoutePlanner::class);
    $request = new IngressRequest(
        deliveryId: 'delivery-future-endpoint',
        method: 'POST',
        host: 'relay.example.test',
        path: '/provider/events/app-123',
        rawQuery: '',
        headers: [],
        body: '{}',
    );

    expect($planner->plan($request)->immediateResponse?->statusCode)->toBe(404);

    $this->withToken('registry-secret')->postJson('/registry/endpoints', [
        'client' => 'relay-test',
        'external_id' => 'future-provider',
        'endpoint_key' => 'future-endpoint',
        'managed' => true,
    ])->assertCreated()->assertJsonPath('endpoint_key', 'future-endpoint');

    expect($planner->plan($request)->immediateResponse?->statusCode)->toBe(204);
});

it('invalidates the old route snapshot when an endpoint key changes', function (): void {
    foreach (['old-endpoint', 'new-endpoint'] as $endpointKey) {
        $this->withToken('registry-secret')->postJson('/registry/endpoints', [
            'client' => 'relay-test',
            'external_id' => 'renamed-provider',
            'endpoint_key' => $endpointKey,
            'managed' => true,
        ])->assertCreated()->assertJsonPath('endpoint_key', $endpointKey);

        if ($endpointKey === 'old-endpoint') {
            TestPathResolver::$endpointKey = $endpointKey;
            expect(app(RegistryRoutePlanner::class)->plan(new IngressRequest(
                deliveryId: 'delivery-before-rename',
                method: 'POST',
                host: 'relay.example.test',
                path: '/provider/events/app-123',
                rawQuery: '',
                headers: [],
                body: '{}',
            ))->immediateResponse?->statusCode)->toBe(204);
        }
    }

    TestPathResolver::$endpointKey = 'old-endpoint';
    expect(app(RegistryRoutePlanner::class)->plan(new IngressRequest(
        deliveryId: 'delivery-after-rename',
        method: 'POST',
        host: 'relay.example.test',
        path: '/provider/events/app-123',
        rawQuery: '',
        headers: [],
        body: '{}',
    ))->immediateResponse?->statusCode)->toBe(404);
});

it('allows explicit invalidation after direct registry mutations', function (): void {
    $endpoint = relayEndpoint();
    $subscriptions = app(SubscribeEndpoint::class);
    $planner = app(RegistryRoutePlanner::class);
    $request = new IngressRequest(
        deliveryId: 'delivery-direct-mutation',
        method: 'POST',
        host: 'relay.example.test',
        path: '/provider/events/app-123',
        rawQuery: '',
        headers: [],
        body: '{}',
    );

    $subscriptions->handle($endpoint->record->endpoint_key, SubscriptionDefinition::matching(
        subscriberId: 'cached',
        subscriptionId: 'cached-messages',
        webhookGroup: 'meta',
        routingScope: 'application',
        routingKey: 'app-123',
        url: 'https://before.example.test/webhooks/meta',
        rules: [],
    ));
    expect($planner->plan($request)->relays[0]->url)->toBe('https://before.example.test/webhooks/meta');

    WebProxyDestination::query()->update(['target' => 'https://after.example.test/webhooks/meta']);
    expect($planner->plan($request)->relays[0]->url)->toBe('https://before.example.test/webhooks/meta');

    app(RegistryRouteCache::class)->invalidateEndpoint($endpoint->record->endpoint_key);
    expect($planner->plan($request)->relays[0]->url)->toBe('https://after.example.test/webhooks/meta');
});

it('selects a matching synchronous reply for Go to execute', function (): void {
    $endpoint = relayEndpoint();

    app(SubscribeEndpoint::class)->handle(
        $endpoint->record->endpoint_key,
        SubscriptionDefinition::matching(
            subscriberId: 'primary-handler',
            subscriptionId: 'primary-handler-messages',
            webhookGroup: 'meta',
            routingScope: 'application',
            routingKey: 'app-123',
            url: 'https://primary.example.test/webhooks/meta',
            rules: ['body.account.id' => ['required', 'in:account-42']],
            type: 'reply',
        ),
    );

    $plan = app(RegistryRoutePlanner::class)->plan(new IngressRequest(
        deliveryId: 'delivery-2',
        method: 'POST',
        host: 'relay.example.test',
        path: '/provider/events/app-123',
        rawQuery: '',
        headers: [],
        body: '{"account":{"id":"account-42"}}',
    ));

    expect($plan->action)->toBe('relay')
        ->and($plan->reply?->subscriberId)->toBe('primary-handler')
        ->and($plan->reply?->url)->toBe('https://primary.example.test/webhooks/meta')
        ->and($plan->relays)->toBe([]);
});

it('prevents competing synchronous replies on the same route', function (): void {
    $endpoint = relayEndpoint();
    $subscriptions = app(SubscribeEndpoint::class);

    $definition = static fn (string $subscriber): SubscriptionDefinition => SubscriptionDefinition::matching(
        subscriberId: $subscriber,
        subscriptionId: $subscriber.'-reply',
        webhookGroup: 'meta',
        routingScope: 'application',
        routingKey: 'app-123',
        url: "https://{$subscriber}.example.test/webhooks/meta",
        rules: [],
        type: 'reply',
    );

    $subscriptions->handle($endpoint->record->endpoint_key, $definition('primary'));

    expect(fn () => $subscriptions->handle(
        $endpoint->record->endpoint_key,
        $definition('competing'),
    ))->toThrow(InvalidArgumentException::class, 'already has a synchronous reply');
});

it('rejects unsafe rules from a remote subscriber', function (): void {
    $endpoint = relayEndpoint();

    $this->withToken('registry-secret')->postJson(
        '/registry/endpoints/'.$endpoint->record->endpoint_key.'/subscriptions',
        [
            'subscriber_id' => 'workspace-42',
            'webhook_group' => 'meta',
            'routing_scope' => 'application',
            'routing_key' => 'app-123',
            'url' => 'https://subscriber.example.test/webhooks/meta',
            'match' => [
                'version' => 'v1',
                'rules' => ['body.account.id' => ['exists:accounts,id']],
            ],
        ],
    )->assertUnprocessable()
        ->assertJsonPath('errors.match.0', 'Match rule [exists] is not allowed.');

    $this->withToken('registry-secret')->postJson(
        '/registry/endpoints/'.$endpoint->record->endpoint_key.'/subscriptions',
        [
            'subscriber_id' => 'workspace-42',
            'webhook_group' => 'meta',
            'routing_scope' => 'application',
            'routing_key' => 'app-123',
            'url' => 'https://subscriber.example.test/webhooks/meta',
            'match' => [
                'version' => 'v1',
                'rules' => ['method' => ['required:ignored']],
            ],
        ],
    )->assertUnprocessable()
        ->assertJsonPath('errors.match.0', 'Match-rule field [method] has invalid [required] parameters.');

    expect(WebProxyDestination::query()->count())->toBe(0);
});

it('registers a Shell reply, selects its hook, and manages its lifecycle', function (): void {
    app(AutomationLanguageRegistry::class)->register('shell', AutomationExecutionTarget::SPINNER, AutomationCapability::ROUTE);
    config()->set('gateway.shell.subscription_token', 'shell-admin-secret');
    $this->withHeader('X-Gateway-Shell-Subscription', 'shell-admin-secret');
    $endpoint = relayEndpoint();
    $grants = grantShellHooks($endpoint->record->endpoint_key,
        ['shell-owner' => ['github/deploy', 'github/deploy-v2']]);
    $route = '/registry/endpoints/'.$endpoint->record->endpoint_key.'/subscriptions';
    $created = $this->withToken('registry-secret')->postJson($route, [
        'subscriber_id' => 'shell-owner',
        'subscription_id' => 'shell-deploy',
        'type' => 'reply',
        'target_kind' => 'shell',
        'hook' => 'github/deploy',
        'webhook_group' => 'meta',
        'routing_scope' => 'application',
        'routing_key' => 'app-123',
        'match' => ['version' => 'v1', 'rules' => ['body.account.id' => ['in:account-42']]],
    ]);
    $created->assertCreated()
        ->assertJsonPath('target_kind', 'shell')
        ->assertJsonPath('hook', 'github/deploy')
        ->assertJsonPath('url', null);
    $created->assertDontSee('gateway-shell://');
    $this->withToken('registry-secret')->getJson($route.'/'.$created->json('id'))
        ->assertOk()->assertJsonPath('hook', 'github/deploy')->assertDontSee('gateway-shell://');
    $this->withToken('registry-secret')->getJson($route.'?routing_scope=application&routing_key=app-123')
        ->assertOk()->assertJsonPath('data.0.hook', 'github/deploy');

    $planner = app(CapabilityAwareRoutePlanner::class);
    $request = static fn (string $account): IngressRequest => new IngressRequest(
        deliveryId: 'shell-delivery',
        method: 'POST',
        host: 'relay.example.test',
        path: '/provider/events/app-123',
        rawQuery: '',
        headers: [],
        body: json_encode(['account' => ['id' => $account]], JSON_THROW_ON_ERROR),
        automationLanguages: ['shell'],
    );
    expect($planner->plan($request('account-42'))->automation?->toArray())->toBe([
        'language' => 'shell', 'source' => 'github/deploy',
    ]);
    expect($planner->plan($request('account-99'))->immediateResponse?->statusCode)->toBe(204);

    $url = $route.'/'.$created->json('id');
    $updated = $this->withToken('registry-secret')->withHeader('If-Match', $created->headers->get('ETag'))
        ->patchJson($url, ['hook' => 'github/deploy-v2']);
    $updated->assertOk()->assertJsonPath('hook', 'github/deploy-v2');
    expect($planner->plan($request('account-42'))->automation?->source)->toBe('github/deploy-v2');

    $paused = $this->withToken('registry-secret')->withHeader('If-Match', $updated->headers->get('ETag'))
        ->patchJson($url.'/status', ['status' => 'paused']);
    $paused->assertOk()->assertJsonPath('status', 'paused');
    expect($planner->plan($request('account-42'))->immediateResponse?->statusCode)->toBe(204);

    $active = $this->withToken('registry-secret')->withHeader('If-Match', $paused->headers->get('ETag'))
        ->patchJson($url.'/status', ['status' => 'active']);
    $active->assertOk()->assertJsonPath('status', 'active');
    expect($planner->plan($request('account-42'))->automation?->source)->toBe('github/deploy-v2');

    $grant = app(ShellHookGrantStore::class)->replace($grants[1]->id, [
        'endpoint_key' => $endpoint->record->endpoint_key,
        'subscriber_id' => 'shell-owner', 'hook' => 'github/deploy-v2', 'enabled' => false,
    ], $grants[1]->revision);
    expect(fn () => $planner->plan($request('account-42')))
        ->toThrow(InvalidArgumentException::class, 'not authorized');
    app(ShellHookGrantStore::class)->replace($grant->id, [
        'endpoint_key' => $endpoint->record->endpoint_key,
        'subscriber_id' => 'shell-owner', 'hook' => 'github/deploy-v2',
    ], $grant->revision);

    $this->withToken('registry-secret')->withHeader('If-Match', $active->headers->get('ETag'))
        ->deleteJson($url)->assertNoContent();
    expect($planner->plan($request('account-42'))->immediateResponse?->statusCode)->toBe(204);
});

it('registers an SSH Shell reply without exposing its write-only credential', function (): void {
    app(AutomationLanguageRegistry::class)->register('shell', AutomationExecutionTarget::SPINNER, AutomationCapability::ROUTE);
    config()->set('gateway.shell.subscription_token', 'shell-admin-secret');
    $this->withHeader('X-Gateway-Shell-Subscription', 'shell-admin-secret');
    $endpoint = relayEndpoint();
    grantShellHooks($endpoint->record->endpoint_key, ['shell-owner' => ['github/deploy']]);
    $route = '/registry/endpoints/'.$endpoint->record->endpoint_key.'/subscriptions';
    $secret = "-----BEGIN OPENSSH PRIVATE KEY-----\ntest-only-key\n-----END OPENSSH PRIVATE KEY-----";
    $hostKey = 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIK5kAfyNpppj3EYei3vB4jC6TMMgrW9P3xOaUbE5bciE';
    $input = [
        'subscriber_id' => 'shell-owner', 'subscription_id' => 'ssh-deploy',
        'type' => 'reply', 'target_kind' => 'shell', 'hook' => 'github/deploy',
        'webhook_group' => 'meta', 'routing_scope' => 'application', 'routing_key' => 'app-123',
        'ssh' => [
            'host' => 'worker.example.test', 'port' => 22, 'user' => 'gateway',
            'host_public_key' => $hostKey,
            'private_key' => $secret,
        ],
    ];
    $this->withToken('registry-secret')->postJson($route, $input)->assertUnprocessable();
    app(ShellHookGrantStore::class)->create([
        'endpoint_key' => $endpoint->record->endpoint_key, 'subscriber_id' => 'shell-owner',
        'hook' => 'github/deploy', 'mode' => 'ssh', 'host' => 'worker.example.test',
        'user' => 'gateway', 'host_public_key' => $hostKey,
    ]);
    $created = $this->withToken('registry-secret')->postJson($route, $input);

    $created->assertCreated()->assertJsonPath('hook', 'github/deploy')
        ->assertJsonPath('ssh.host', 'worker.example.test')
        ->assertJsonMissingPath('ssh.private_key');
    $created->assertDontSee($secret);
    $this->withToken('registry-secret')->postJson($route, $input)->assertUnprocessable();
    $this->withToken('registry-secret')->getJson($route.'/'.$created->json('id'))
        ->assertOk()->assertDontSee($secret);

    $plan = app(CapabilityAwareRoutePlanner::class)->plan(new IngressRequest(
        deliveryId: 'ssh-shell', method: 'POST', host: 'relay.example.test',
        path: '/provider/events/app-123', rawQuery: '', headers: [], body: '{}',
        automationLanguages: ['shell'],
    ));
    expect($plan->automation?->source)->toStartWith('ssh:');
    expect(json_encode($plan->toArray(), JSON_THROW_ON_ERROR))->not->toContain($secret, 'worker.example.test');
});

it('rotates and revokes an SSH Shell credential through authenticated subscription lifecycle', function (): void {
    app(AutomationLanguageRegistry::class)->register('shell', AutomationExecutionTarget::SPINNER, AutomationCapability::ROUTE);
    config()->set('gateway.shell.subscription_token', 'shell-admin-secret');
    $this->withHeader('X-Gateway-Shell-Subscription', 'shell-admin-secret');
    $endpoint = relayEndpoint();
    app(ShellHookGrantStore::class)->create([
        'endpoint_key' => $endpoint->record->endpoint_key, 'subscriber_id' => 'shell-owner',
        'hook' => 'github/deploy', 'mode' => 'ssh', 'host' => 'worker.example.test',
        'user' => 'gateway',
        'host_public_key' => 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIK5kAfyNpppj3EYei3vB4jC6TMMgrW9P3xOaUbE5bciE',
    ]);
    $route = '/registry/endpoints/'.$endpoint->record->endpoint_key.'/subscriptions';
    $firstKey = "-----BEGIN OPENSSH PRIVATE KEY-----\nfirst-key\n-----END OPENSSH PRIVATE KEY-----";
    $nextKey = "-----BEGIN OPENSSH PRIVATE KEY-----\nnext-key\n-----END OPENSSH PRIVATE KEY-----";
    $created = $this->withToken('registry-secret')->postJson($route, [
        'subscriber_id' => 'shell-owner', 'subscription_id' => 'ssh-deploy',
        'type' => 'reply', 'target_kind' => 'shell', 'hook' => 'github/deploy',
        'webhook_group' => 'meta', 'routing_scope' => 'application', 'routing_key' => 'app-123',
        'ssh' => ['host' => 'worker.example.test', 'user' => 'gateway',
            'host_public_key' => 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIK5kAfyNpppj3EYei3vB4jC6TMMgrW9P3xOaUbE5bciE',
            'private_key' => $firstKey],
    ]);
    $created->assertCreated();
    $source = app(CapabilityAwareRoutePlanner::class)->plan(new IngressRequest(
        deliveryId: 'ssh-shell', method: 'POST', host: 'relay.example.test',
        path: '/provider/events/app-123', rawQuery: '', headers: [], body: '{}',
        automationLanguages: ['shell'],
    ))->automation->source;
    $private = '/_internal/gateway/shell/executions/'.substr($source, 4);
    $audit = '/configurations/shell.ssh-execution/'.substr($source, 4).'/revisions';
    $this->withToken('registry-secret')->getJson($audit)->assertOk()->assertJsonCount(1, 'data');
    $this->withHeader('X-Gateway-Internal', 'wrong')->getJson($private)->assertNotFound();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')->getJson($private)
        ->assertOk()->assertJsonPath('private_key', $firstKey);

    $url = $route.'/'.$created->json('id');
    $rotated = $this->withToken('registry-secret')->withHeader('If-Match', $created->headers->get('ETag'))
        ->postJson($url.'/ssh/rotate-key', ['private_key' => $nextKey]);
    $rotated->assertOk()->assertJsonPath('ssh.credential_version', 2)->assertDontSee($nextKey);
    $this->withToken('registry-secret')->getJson($audit)->assertOk()->assertJsonCount(2, 'data')
        ->assertDontSee($firstKey)->assertDontSee($nextKey);
    $this->withToken('registry-secret')->withHeader('If-Match', $created->headers->get('ETag'))
        ->postJson($url.'/ssh/rotate-key', ['private_key' => $firstKey])->assertStatus(412);
    $this->withHeader('X-Gateway-Internal', 'internal-secret')->getJson($private)
        ->assertOk()->assertJsonPath('private_key', $nextKey)->assertDontSee($firstKey);

    $this->withToken('registry-secret')->withHeader('If-Match', $rotated->headers->get('ETag'))
        ->deleteJson($url)->assertNoContent();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')->getJson($private)->assertNotFound();
    $this->withToken('registry-secret')->getJson($audit)->assertOk()->assertJsonCount(3, 'data');
});

it('shares reply cardinality between HTTP and Shell subscriptions', function (): void {
    app(AutomationLanguageRegistry::class)->register('shell', AutomationExecutionTarget::SPINNER, AutomationCapability::ROUTE);
    config()->set('gateway.shell.subscription_token', 'shell-admin-secret');
    $this->withHeader('X-Gateway-Shell-Subscription', 'shell-admin-secret');
    $endpoint = relayEndpoint();
    grantShellHooks($endpoint->record->endpoint_key, [
        'shell-owner' => ['github/deploy'], 'other-shell-owner' => ['github/other'],
    ]);
    $route = '/registry/endpoints/'.$endpoint->record->endpoint_key.'/subscriptions';
    $base = [
        'webhook_group' => 'meta',
        'routing_scope' => 'application',
        'routing_key' => 'app-123',
        'type' => 'reply',
    ];

    $shell = $this->withToken('registry-secret')->postJson($route, [
        ...$base, 'subscriber_id' => 'shell-owner', 'target_kind' => 'shell', 'hook' => 'github/deploy',
    ]);
    $shell->assertCreated();
    $this->withToken('registry-secret')->postJson($route, [
        ...$base, 'subscriber_id' => 'http-owner', 'url' => 'https://example.test/hook',
    ])->assertUnprocessable()->assertJsonPath('errors.subscription.0', 'This route already has a synchronous reply subscription.');
    $this->withToken('registry-secret')->postJson($route, [
        ...$base, 'subscriber_id' => 'other-shell-owner', 'target_kind' => 'shell', 'hook' => 'github/other',
    ])->assertUnprocessable();

    $paused = $this->withToken('registry-secret')->withHeader('If-Match', $shell->headers->get('ETag'))
        ->patchJson($route.'/'.$shell->json('id').'/status', ['status' => 'paused']);
    $paused->assertOk();
    $this->withToken('registry-secret')->postJson($route, [
        ...$base, 'subscriber_id' => 'http-owner', 'url' => 'https://example.test/hook',
    ])->assertCreated();
    $this->withToken('registry-secret')->withHeader('If-Match', $paused->headers->get('ETag'))
        ->patchJson($route.'/'.$shell->json('id').'/status', ['status' => 'active'])
        ->assertUnprocessable();
});

it('keeps HTTP relay fan-out alongside a Shell reply and requires Spinner capability', function (): void {
    config()->set('gateway.shell.subscription_token', 'shell-admin-secret');
    $this->withHeader('X-Gateway-Shell-Subscription', 'shell-admin-secret');
    $endpoint = relayEndpoint();
    grantShellHooks($endpoint->record->endpoint_key, ['shell-owner' => ['github/deploy']]);
    $route = '/registry/endpoints/'.$endpoint->record->endpoint_key.'/subscriptions';
    $base = [
        'webhook_group' => 'meta', 'routing_scope' => 'application', 'routing_key' => 'app-123',
    ];
    $this->withToken('registry-secret')->postJson($route, [
        ...$base, 'subscriber_id' => 'shell-owner', 'type' => 'reply',
        'target_kind' => 'shell', 'hook' => 'github/deploy',
    ])->assertCreated();
    $this->withToken('registry-secret')->postJson($route, [
        ...$base, 'subscriber_id' => 'relay-owner', 'type' => 'relay',
        'url' => 'https://relay.example.test/events',
    ])->assertCreated();

    $request = static fn (array $languages): IngressRequest => new IngressRequest(
        deliveryId: 'shell-relay', method: 'POST', host: 'relay.example.test',
        path: '/provider/events/app-123', rawQuery: '', headers: [], body: '{}',
        automationLanguages: $languages,
    );
    $plan = app(CapabilityAwareRoutePlanner::class)->plan($request(['shell']));
    expect($plan->action)->toBe('automation')
        ->and($plan->automation?->source)->toBe('github/deploy')
        ->and($plan->relays)->toHaveCount(1)
        ->and($plan->relays[0]->url)->toBe('https://relay.example.test/events');
    expect(fn () => app(CapabilityAwareRoutePlanner::class)->plan($request([])))
        ->toThrow(RuntimeException::class, 'not available in this Spinner runtime');
});

it('offers the same Shell reply definition to trusted PHP callers', function (): void {
    $endpoint = relayEndpoint();
    grantShellHooks($endpoint->record->endpoint_key, ['php-owner' => ['github/deploy.ps1']]);
    $destination = app(SubscribeEndpoint::class)->handle(
        $endpoint->record->endpoint_key,
        SubscriptionDefinition::shellReply(
            subscriberId: 'php-owner',
            subscriptionId: 'php-deploy',
            webhookGroup: 'meta',
            routingScope: 'application',
            routingKey: 'app-123',
            hook: 'github/deploy.ps1',
            rules: ['method' => ['in:POST']],
        ),
    );
    expect($destination->target)->toBe('gateway-shell://local/github/deploy.ps1');
    $plan = app(RegistryRoutePlanner::class)->plan(new IngressRequest(
        deliveryId: 'php-shell', method: 'POST', host: 'relay.example.test',
        path: '/provider/events/app-123', rawQuery: '', headers: [], body: '{}',
    ));
    expect($plan->automation?->source)->toBe('github/deploy.ps1');
});

it('rejects unsafe Shell reply registrations and hook changes', function (): void {
    config()->set('gateway.shell.subscription_token', 'shell-admin-secret');
    $endpoint = relayEndpoint();
    $route = '/registry/endpoints/'.$endpoint->record->endpoint_key.'/subscriptions';
    $base = [
        'subscriber_id' => 'shell-owner',
        'type' => 'reply',
        'target_kind' => 'shell',
        'hook' => 'github/deploy',
        'webhook_group' => 'meta',
        'routing_scope' => 'application',
        'routing_key' => 'app-123',
    ];
    $this->withToken('registry-secret')->postJson($route, $base)->assertForbidden();
    $this->withHeader('X-Gateway-Shell-Subscription', 'shell-admin-secret');
    $this->withToken('registry-secret')->postJson($route, $base)->assertUnprocessable()
        ->assertJsonPath('errors.subscription.0', 'The Shell hook is not authorized for this endpoint and subscriber.');
    grantShellHooks($endpoint->record->endpoint_key, ['shell-owner' => ['github/deploy']]);
    foreach ([
        ['hook' => '../escape'],
        ['type' => 'relay'],
        ['url' => 'https://example.test/hook'],
        ['profile' => 'proxy'],
        ['credential' => 'private-key'],
        ['metadata' => ['private_key' => 'private-key']],
    ] as $change) {
        $this->withToken('registry-secret')->postJson($route, [...$base, ...$change])->assertUnprocessable();
    }
    $created = $this->withToken('registry-secret')->postJson($route, $base);
    $created->assertCreated();
    $url = $route.'/'.$created->json('id');
    $this->withHeader('X-Gateway-Shell-Subscription', 'wrong-token');
    $this->withToken('registry-secret')->withHeader('If-Match', $created->headers->get('ETag'))
        ->patchJson($url, ['hook' => 'github/deploy'])->assertForbidden();
    $this->withToken('registry-secret')->withHeader('If-Match', $created->headers->get('ETag'))
        ->patchJson($url.'/status', ['status' => 'paused'])->assertForbidden();
    $this->withToken('registry-secret')->withHeader('If-Match', $created->headers->get('ETag'))
        ->patchJson($url.'/match', [
            'version' => 'v1',
            'operations' => [['op' => 'add', 'field' => 'body.id', 'rules' => ['required']]],
        ])->assertForbidden();
    $this->withToken('registry-secret')->withHeader('If-Match', $created->headers->get('ETag'))
        ->deleteJson($url)->assertForbidden();
    $this->withHeader('X-Gateway-Shell-Subscription', 'shell-admin-secret');
    foreach ([
        ['hook' => '../escape'],
        ['hook' => 'github/other'],
        ['url' => 'https://example.test/hook'],
        ['type' => 'relay'],
        ['profile' => null],
    ] as $change) {
        $this->withToken('registry-secret')->withHeader('If-Match', $created->headers->get('ETag'))
            ->patchJson($url, $change)->assertUnprocessable();
    }
});
