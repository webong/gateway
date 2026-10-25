<?php

declare(strict_types=1);

use Webong\Gateway\Observations\GatewayObservationRegistry;
use Webong\Gateway\Protocols\GatewayObservation;

it('parses a bounded language-neutral observation and fans it out to registered recorders', function (): void {
    $observation = GatewayObservation::fromArray([
        'version' => 'v1',
        'id' => 'dns-query-1',
        'type' => 'dns.query',
        'occurred_at' => '2026-10-25T12:00:00+00:00',
        'attributes' => ['mode' => 'forwarded', 'response_name' => 'NOERROR'],
    ]);
    $recorded = [];
    $registry = new GatewayObservationRegistry;
    $registry->register('dns.query', function (GatewayObservation $value) use (&$recorded): void {
        $recorded[] = 'history:'.$value->id;
    });
    $registry->register('dns.query', function (GatewayObservation $value) use (&$recorded): void {
        $recorded[] = 'analytics:'.$value->attributes['response_name'];
    });

    $registry->record($observation);

    expect($recorded)->toBe(['history:dns-query-1', 'analytics:NOERROR']);
});

it('rejects unknown observation versions, types, and unbounded attributes', function (array $overrides): void {
    $payload = [
        'version' => 'v1',
        'id' => 'dns-query-1',
        'type' => 'dns.query',
        'occurred_at' => '2026-10-25T12:00:00+00:00',
        'attributes' => ['mode' => 'managed'],
        ...$overrides,
    ];

    expect(fn () => GatewayObservation::fromArray($payload))->toThrow(InvalidArgumentException::class);
})->with([
    'version' => [['version' => 'v2']],
    'type' => [['type' => 'DNS query']],
    'time' => [['occurred_at' => '']],
    'attribute' => [['attributes' => ['payload' => str_repeat('x', 4097)]]],
]);
