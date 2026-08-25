<?php

declare(strict_types=1);

use Webong\WebProxy\Endpoint;
use Webong\WebProxy\EndpointDefinition;
use Webong\WebProxy\EnsureEndpoint;
use Webong\WebProxy\Models\WebProxyDestination;
use Webong\WebProxy\Models\WebProxyEndpoint;
use Webong\WebRelay\Protocol\IngressRequest;
use Webong\WebRelay\RegistryRoutePlanner;
use Webong\WebRelay\SubscribeEndpoint;
use Webong\WebRelay\SubscriptionDefinition;
use Webong\WebRelay\Tests\Support\TestPathResolver;

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
