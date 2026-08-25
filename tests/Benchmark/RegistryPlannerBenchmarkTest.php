<?php

declare(strict_types=1);

use Webong\WebProxy\EndpointDefinition;
use Webong\WebProxy\EnsureEndpoint;
use Webong\WebRelay\Protocol\IngressRequest;
use Webong\WebRelay\RegistryRoutePlanner;
use Webong\WebRelay\SubscribeEndpoint;
use Webong\WebRelay\SubscriptionDefinition;
use Webong\WebRelay\Tests\Support\TestPathResolver;

it('benchmarks cached planning with realistic subscription counts', function (): void {
    $subscriberCount = max(1, (int) getenv('WEB_RELAY_BENCH_SUBSCRIBERS') ?: 100);
    $iterations = max(1, (int) getenv('WEB_RELAY_BENCH_ITERATIONS') ?: 250);

    $endpoint = app(EnsureEndpoint::class)->handle(new EndpointDefinition(
        client: 'relay-test',
        externalId: 'benchmark-provider',
        signingSecret: 'benchmark-secret',
        verificationToken: 'benchmark-token',
        endpointKey: 'benchmark-endpoint',
        credentialOwnerId: 'benchmark-owner',
        managed: false,
    ));
    TestPathResolver::$endpointKey = $endpoint->record->endpoint_key;

    $subscriptions = app(SubscribeEndpoint::class);
    for ($index = 0; $index < $subscriberCount; $index++) {
        $account = $index % 2 === 0 ? 'account-42' : 'account-other';
        $subscriptions->handle($endpoint->record->endpoint_key, SubscriptionDefinition::matching(
            subscriberId: 'subscriber-'.$index,
            subscriptionId: 'benchmark-'.$index,
            webhookGroup: 'benchmark',
            routingScope: 'application',
            routingKey: 'app-123',
            url: 'https://subscriber-'.$index.'.example.test/webhook',
            rules: [
                'headers.x-event-type' => ['required', 'in:message.created'],
                'body.account.id' => ['required', 'in:'.$account],
            ],
        ));
    }

    $request = new IngressRequest(
        deliveryId: 'benchmark-delivery',
        method: 'POST',
        host: 'relay.example.test',
        path: '/provider/events/app-123',
        rawQuery: '',
        headers: ['X-Event-Type' => ['message.created']],
        body: '{"account":{"id":"account-42"}}',
    );
    $planner = app(RegistryRoutePlanner::class);
    $expectedMatches = (int) ceil($subscriberCount / 2);

    // Fill both path and route caches before collecting samples.
    expect($planner->plan($request)->relays)->toHaveCount($expectedMatches);

    $samples = [];
    for ($iteration = 0; $iteration < $iterations; $iteration++) {
        $started = hrtime(true);
        $plan = $planner->plan($request);
        $samples[] = (hrtime(true) - $started) / 1_000_000;

        expect($plan->relays)->toHaveCount($expectedMatches);
    }

    sort($samples);
    $percentile = static function (array $values, float $percentile): float {
        $index = max(0, (int) ceil(count($values) * $percentile) - 1);

        return $values[$index];
    };
    $mean = array_sum($samples) / count($samples);
    $p95 = $percentile($samples, 0.95);

    fwrite(STDOUT, sprintf(
        "\nplanner benchmark: subscribers=%d matches=%d iterations=%d mean=%.3fms p50=%.3fms p95=%.3fms p99=%.3fms\n",
        $subscriberCount,
        $expectedMatches,
        $iterations,
        $mean,
        $percentile($samples, 0.50),
        $p95,
        $percentile($samples, 0.99),
    ));

    $targetRps = max(0, (int) getenv('WEB_RELAY_BENCH_TARGET_RPS'));
    if ($targetRps > 0) {
        $headroom = max(1.0, (float) (getenv('WEB_RELAY_BENCH_HEADROOM') ?: 1.5));
        $workers = max(1, (int) ceil($targetRps * ($p95 / 1000) * $headroom));

        fwrite(STDOUT, sprintf(
            "capacity estimate: target=%dreq/s headroom=%.2fx recommended_warm_workers=%d\n",
            $targetRps,
            $headroom,
            $workers,
        ));
    }
});
