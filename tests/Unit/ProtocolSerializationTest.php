<?php

declare(strict_types=1);

use Webong\Gateway\Protocols\Automation;
use Webong\Gateway\Protocols\Delivery;
use Webong\Gateway\Protocols\HttpTransport;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\Response;
use Webong\Gateway\Protocols\RoutePlan;
use Webong\Gateway\Protocols\ScheduleCommand;

it('serializes empty response headers as a JSON map for Go', function (): void {
    $json = json_encode(
        RoutePlan::respond(new Response(statusCode: 204))->toArray(),
        JSON_THROW_ON_ERROR,
    );
    $decoded = json_decode($json, false, 512, JSON_THROW_ON_ERROR);

    expect($decoded->immediate_response->headers)->toBeObject()
        ->and((array) $decoded->immediate_response->headers)->toBe([]);
});

it('serializes a Go-executed automation route plan', function (): void {
    expect(RoutePlan::automate(new Automation(
        language: 'test-script',
        source: 'gateway.respond("ok", 200);',
    ))->toArray())->toBe([
        'version' => 'v1',
        'action' => 'automation',
        'automation' => [
            'language' => 'test-script',
            'source' => 'gateway.respond("ok", 200);',
        ],
    ]);
});

it('allows PHP to choose a structured shell completion notification destination', function (): void {
    $plan = RoutePlan::automate(
        new Automation('shell', 'github/deploy'),
        shellNotification: new Delivery(url: 'https://operator.example.test/shell-runs'),
    );

    expect($plan->toArray()['shell_notification'])->toBe([
        'url' => 'https://operator.example.test/shell-runs',
    ]);

    expect(fn (): RoutePlan => RoutePlan::automate(
        new Automation('javascript', 'run()'),
        shellNotification: new Delivery(url: 'https://operator.example.test/shell-runs'),
    ))->toThrow(InvalidArgumentException::class);
    expect(fn (): RoutePlan => RoutePlan::automate(
        new Automation('shell', 'github/deploy'),
        shellNotification: new Delivery(url: 'https://operator.example.test/shell-runs', body: 'arbitrary'),
    ))->toThrow(InvalidArgumentException::class);
});

it('allows an installed extension to define its language identifier', function (): void {
    expect((new Automation('acme_script', 'run()'))->toArray())->toBe([
        'language' => 'acme_script',
        'source' => 'run()',
    ]);
});

it('accepts the active Spinner language catalog from the bridge', function (): void {
    $request = IngressRequest::fromArray([
        'delivery_id' => 'delivery-1',
        'method' => 'POST',
        'path' => '/hook',
        'body' => base64_encode(''),
        'runtime' => [
            'automation_languages' => ['lua', 'javascript', 'lua'],
        ],
    ]);

    expect($request->automationLanguages)->toBe(['javascript', 'lua']);
});

it('serializes asynchronous relays with an immediate planner response', function (): void {
    expect(RoutePlan::relay(
        relays: [new Delivery(url: 'https://subscriber.example.test/hook')],
        immediateResponse: new Response(statusCode: 202, body: 'scripted'),
    )->toArray())->toEqual([
        'version' => 'v1',
        'action' => 'relay',
        'immediate_response' => [
            'status_code' => 202,
            'headers' => (object) [],
            'body' => base64_encode('scripted'),
        ],
        'relays' => [[
            'url' => 'https://subscriber.example.test/hook',
        ]],
    ]);
});

it('serializes endpoint network settings without exposing a profile identity', function (): void {
    $delivery = new Delivery(
        url: 'https://subscriber.example.test/hook',
        transport: new HttpTransport(
            httpProxy: 'http://proxy-user:proxy-pass@proxy.example.test:8080',
            httpsProxy: 'socks5h://proxy.example.test:1080',
            noProxy: ['.internal.example.test'],
            streamHeaders: ['Authorization' => ['Bearer source-secret']],
            connectTimeoutSeconds: 5,
            requestTimeoutSeconds: 15,
        ),
    );

    expect($delivery->toArray())->toBe([
        'url' => 'https://subscriber.example.test/hook',
        'transport' => [
            'proxy' => [
                'http' => 'http://proxy-user:proxy-pass@proxy.example.test:8080',
                'https' => 'socks5h://proxy.example.test:1080',
                'no_proxy' => ['.internal.example.test'],
            ],
            'connect_timeout_seconds' => 5.0,
            'request_timeout_seconds' => 15.0,
            'stream_headers' => ['Authorization' => ['Bearer source-secret']],
        ],
    ])->and(Delivery::fromArray($delivery->toArray()))->toEqual($delivery);
});

it('does not permit a static source profile to replace the SSE resume marker', function (): void {
    expect(fn () => new HttpTransport(streamHeaders: ['Last-Event-ID' => ['stale']]))
        ->toThrow(InvalidArgumentException::class);
});

it('round-trips declarative script schedule commands', function (): void {
    $command = new ScheduleCommand(
        key: 'daily-report',
        name: 'Daily report',
        cron: '0 9 * * *',
        language: 'javascript',
        source: 'gateway.respond("done", 200)',
        timezone: 'Africa/Lagos',
        payload: ['team' => 'ops'],
    );

    expect(ScheduleCommand::fromArray($command->toArray()))->toEqual($command);
});
