<?php

declare(strict_types=1);

use Webong\WebProxy\Endpoint;
use Webong\WebProxy\EndpointDefinition;
use Webong\WebProxy\EnsureEndpoint;
use Webong\WebProxy\Models\WebProxyDestination;
use Webong\WebProxy\Models\WebProxyEndpoint;
use Webong\WebRelay\Protocol\IngressRequest;
use Webong\WebRelay\RegistryRoutePlanner;
use Webong\WebRelay\RegistryRouteCache;
use Webong\WebRelay\SubscribeEndpoint;
use Webong\WebRelay\SubscriptionDefinition;
use Webong\WebRelay\Tests\Support\TestPathResolver;

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
        ->postJson('/web-relay/endpoints/legacy/subscriptions', [])
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

    $response->assertCreated();
    expect($response->json('match.rules'))->toBe([
        'headers.x-event-type' => ['required', 'in:message.created'],
        'body.account.id' => ['required', 'in:account-42'],
    ]);

    $destination = WebProxyDestination::query()->sole();
    expect($destination->metadata['_web_relay_match'])->toBe([
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
});
