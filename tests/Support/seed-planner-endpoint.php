<?php

declare(strict_types=1);

/**
 * Seed a managed endpoint and relay subscription into the configured Planner
 * database. This script boots the real application so it reuses the exact
 * `EnsureEndpoint` / `SubscribeEndpoint` framework logic and metadata format.
 *
 * Configuration is read from the environment (set by the caller before exec):
 *   GATEWAY_SEED_ENDPOINT_KEY, GATEWAY_SEED_EXTERNAL_ID, GATEWAY_SEED_ROUTING_SCOPE,
 *   GATEWAY_SEED_ROUTING_KEY, GATEWAY_SEED_SUBSCRIBER_ID, GATEWAY_SEED_URL.
 *
 * Prints the created endpoint record id on success.
 */

$root = dirname(__DIR__, 2);

require $root.'/vendor/autoload.php';

$app = require $root.'/app/bootstrap.php';
$app->make(Illuminate\Contracts\Console\Kernel::class)->bootstrap();

$endpointKey = (string) (getenv('GATEWAY_SEED_ENDPOINT_KEY') ?: 'http-smoke-endpoint');
$externalId = (string) (getenv('GATEWAY_SEED_EXTERNAL_ID') ?: 'provider-app-123');
$routingScope = (string) (getenv('GATEWAY_SEED_ROUTING_SCOPE') ?: 'application');
$routingKey = (string) (getenv('GATEWAY_SEED_ROUTING_KEY') ?: 'app-123');
$subscriberId = (string) (getenv('GATEWAY_SEED_SUBSCRIBER_ID') ?: 'workspace-42');
$url = (string) (getenv('GATEWAY_SEED_URL') ?: 'https://subscriber.example.test/hook');

$endpoint = $app->make(Webong\Proxy\EnsureEndpoint::class)->handle(new Webong\Proxy\EndpointDefinition(
    client: 'gateway',
    externalId: $externalId,
    signingSecret: 'provider-secret',
    verificationToken: 'verification-token',
    endpointKey: $endpointKey,
    credentialOwnerId: 'endpoint-owner',
    managed: true,
));

$app->make(Webong\Gateway\Laravel\SubscribeEndpoint::class)->handle(
    $endpointKey,
    Webong\Gateway\Web\WebSubscriptionDefinition::matching(
        subscriberId: $subscriberId,
        subscriptionId: $subscriberId.'-messages',
        webhookGroup: 'meta',
        routingScope: $routingScope,
        routingKey: $routingKey,
        url: $url,
        rules: [],
    ),
);

echo $endpoint->record->id;
