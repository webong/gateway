<?php

declare(strict_types=1);

use Illuminate\Support\Facades\Artisan;
use Webong\Gateway\Protocol\MatchRules;
use Webong\Gateway\RegistryRouteCache;
use Webong\Gateway\SubscribeEndpoint;
use Webong\Gateway\SubscriptionDefinition;
use Webong\WebProxy\EndpointDefinition;
use Webong\WebProxy\EndpointRegistry;

Artisan::command('gateway:token', function (): void {
    $this->line((string) config('gateway.registry_token'));
})->purpose('Print the Router management API token');

Artisan::command('gateway:endpoint {key}', function (string $key): void {
    if (! preg_match('/^[A-Za-z0-9_.-]{1,64}$/D', $key)) {
        throw new InvalidArgumentException('Endpoint keys must contain 1-64 letters, digits, dots, underscores, or hyphens.');
    }

    app(EndpointRegistry::class)->ensure(new EndpointDefinition(
        client: 'gateway',
        externalId: $key,
        signingSecret: null,
        verificationToken: null,
        endpointKey: $key,
        managed: true,
    ));
    app(RegistryRouteCache::class)->invalidateEndpoint($key);

    $this->info("Endpoint ready at /hooks/{$key}");
})->purpose('Create an endpoint in the bundled Planner');

Artisan::command('gateway:subscribe {key} {subscriber} {url}', function (
    string $key,
    string $subscriber,
    string $url,
): void {
    if (! preg_match('/^[A-Za-z0-9_.-]{1,64}$/D', $key)) {
        throw new InvalidArgumentException('Invalid endpoint key.');
    }

    app(SubscribeEndpoint::class)->handle($key, new SubscriptionDefinition(
        subscriberId: $subscriber,
        subscriptionId: $subscriber,
        webhookGroup: 'default',
        routingScope: 'path',
        routingKey: "/hooks/{$key}",
        url: $url,
        match: MatchRules::any(),
    ));

    $this->info("Subscribed {$subscriber} to /hooks/{$key}");
})->purpose('Add an HTTP subscriber to a bundled Planner endpoint');
