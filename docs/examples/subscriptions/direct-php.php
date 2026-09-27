<?php

declare(strict_types=1);

use Webong\Gateway\SubscribeEndpoint;
use Webong\Gateway\SubscriptionDefinition;
use Webong\WebProxy\EndpointDefinition;
use Webong\WebProxy\EndpointRegistry;
use Webong\WebProxy\ProfileDefinition;

// Store the real URL in application configuration or a secret manager.
$proxyUrl = config('services.webhooks.proxy_url');
if (! is_string($proxyUrl) || $proxyUrl === '') {
    throw new \LogicException('Configure services.webhooks.proxy_url first.');
}

$endpoint = app(EndpointRegistry::class)->ensure(new EndpointDefinition(
    client: 'meta-messenger',
    externalId: 'app-123',
    signingSecret: config('services.meta.signing_secret'),
    verificationToken: config('services.meta.verification_token'),
    endpointKey: 'meta-messenger',
    credentialOwnerId: 'workspace-42',
));

// The name is arbitrary and is scoped to this endpoint. The configuration is
// encrypted at rest by webong/proxy.
$endpoint->registerProfile(new ProfileDefinition(
    name: 'proxy',
    configuration: [
        'proxy' => [
            'http' => $proxyUrl,
            'https' => $proxyUrl,
            'no' => ['.internal.example.test'],
        ],
        'options' => [
            'connect_timeout' => 5,
            'timeout' => 15,
        ],
    ],
));

$subscriptions = app(SubscribeEndpoint::class);

// No profile: Spinner uses its default outbound HTTP configuration.
$subscriptions->handle($endpoint->record->endpoint_key, SubscriptionDefinition::matching(
    subscriberId: 'audit-direct',
    subscriptionId: 'audit-direct-messages',
    webhookGroup: 'meta',
    routingScope: 'application',
    routingKey: 'app-123',
    url: 'https://direct-subscriber.example.test/webhooks/meta',
    rules: [
        'headers.x-event-type' => ['required', 'in:message.created'],
        'body.account.id' => ['required', 'string', 'in:account-42'],
    ],
));

// Named profile: Planner resolves it and Spinner applies its proxy and timeout
// settings only when this subscription matches.
$subscriptions->handle($endpoint->record->endpoint_key, SubscriptionDefinition::matching(
    subscriberId: 'audit-proxied',
    subscriptionId: 'audit-proxied-messages',
    webhookGroup: 'meta',
    routingScope: 'application',
    routingKey: 'app-123',
    url: 'https://proxied-subscriber.example.test/webhooks/meta',
    profile: 'proxy',
    rules: [
        'headers.x-event-type' => ['required', 'in:message.created'],
        'body.account.id' => ['required', 'string', 'in:account-42'],
    ],
));
