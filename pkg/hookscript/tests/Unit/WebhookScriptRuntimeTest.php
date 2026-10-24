<?php

declare(strict_types=1);

use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\HookScript\HookScriptExecutionException;
use Webong\Gateway\HookScript\HookScriptRuntime;
use Symfony\Component\Process\Process;

function webhookScriptRequest(
    string $query = 'product=apple',
    array $headers = ['Content-Type' => ['application/json']],
    string $body = '{"id":"evt_123"}',
): IngressRequest {
    return new IngressRequest(
        deliveryId: 'delivery-1',
        method: 'POST',
        host: 'hooks.example.test',
        path: '/incoming',
        rawQuery: $query,
        headers: $headers,
        body: $body,
    );
}

it('does not leak vendor deprecations while loading the interpreter', function (): void {
    $autoload = dirname(__DIR__, 4).'/vendor/autoload.php';
    $process = new Process([PHP_BINARY, '-d', 'error_reporting=-1', '-r', <<<'PHP'
        require $argv[1];
        set_error_handler(static function (int $severity, string $message): bool {
            if (in_array($severity, [E_DEPRECATED, E_USER_DEPRECATED], true)) {
                fwrite(STDERR, $message."\n");
            }

            return true;
        });
        $request = new Webong\Gateway\Protocols\IngressRequest(
            deliveryId: 'delivery-1',
            method: 'POST',
            host: 'hooks.example.test',
            path: '/incoming',
            rawQuery: '',
            headers: [],
            body: '',
        );
        (new Webong\Gateway\HookScript\HookScriptRuntime)->execute("respond('ok');", $request);
        PHP, $autoload]);

    $process->mustRun();

    expect($process->getErrorOutput())->toBe('');
});

it('executes Webhook.site-style Primi syntax', function (): void {
    $outcome = (new HookScriptRuntime)->execute(<<<'PRIMI'
        products = [
            'apple': ['price': 10],
            'cake': ['price': 550]
        ];
        selected = var('request.query.product');
        respond('Price {}'.format(products[selected]['price']), 202, ['X-Engine: Primi']);
        PRIMI, webhookScriptRequest());

    expect($outcome->response?->statusCode)->toBe(202)
        ->and($outcome->response?->body)->toBe('Price 10')
        ->and($outcome->response?->headers)->toBe(['X-Engine' => ['Primi']]);
});

it('exposes bounded response, variable, and delivery capabilities', function (): void {
    $outcome = (new HookScriptRuntime)->execute(<<<'PRIMI'
        payload = json_decode(var('request.content'));
        set('result.id', payload['id']);
        set('result.copy', variables['result.id']);
        variables['direct'] = 'visible';
        set('result.direct', var('direct'));
        forward('https://subscriber.example.test/hook', json_encode(payload), 'POST', ['Content-Type: application/json']);
        set_status(202);
        set_header('X-Script', 'primi');
        set_content('queued');
        PRIMI, webhookScriptRequest());

    expect($outcome->variables['result.id'])->toBe('evt_123')
        ->and($outcome->variables['result.copy'])->toBe('evt_123')
        ->and($outcome->variables['result.direct'])->toBe('visible')
        ->and($outcome->deliveries)->toHaveCount(1)
        ->and($outcome->deliveries[0]->url)->toBe('https://subscriber.example.test/hook')
        ->and($outcome->deliveries[0]->body)->toBe('{"id":"evt_123"}')
        ->and($outcome->response?->statusCode)->toBe(202)
        ->and($outcome->response?->headers)->toBe(['X-Script' => ['primi']])
        ->and($outcome->response?->body)->toBe('queued');
});

it('emits declarative schedule commands', function (): void {
    $outcome = (new HookScriptRuntime)->execute(<<<'PRIMI'
        schedule([
            'key': 'daily-report',
            'name': 'Daily report',
            'cron': '0 9 * * *',
            'timezone': 'Africa/Lagos',
            'language': 'javascript',
            'source': "gateway.respond('done', 200)",
            'payload': ['team': 'ops']
        ]);
        PRIMI, webhookScriptRequest());

    expect($outcome->schedules)->toHaveCount(1)
        ->and($outcome->schedules[0]->key)->toBe('daily-report')
        ->and($outcome->schedules[0]->timezone)->toBe('Africa/Lagos')
        ->and($outcome->schedules[0]->payload)->toBe(['team' => 'ops']);
});

it('does not expose undeclared host capabilities', function (): void {
    expect(fn () => (new HookScriptRuntime)->execute(
        "store('secret', 'value');",
        webhookScriptRequest(),
    ))->toThrow(HookScriptExecutionException::class);
});

it('enforces the configured source limit', function (): void {
    expect(fn () => (new HookScriptRuntime(maxSourceBytes: 8))->execute(
        "respond('too long');",
        webhookScriptRequest(),
    ))->toThrow(HookScriptExecutionException::class, 'exceeds the configured limit');
});

it('interrupts scripts that exceed the configured deadline', function (): void {
    $startedAt = microtime(true);

    expect(fn () => (new HookScriptRuntime(timeoutSeconds: 1))->execute(
        'while (true) {}',
        webhookScriptRequest(),
    ))->toThrow(HookScriptExecutionException::class, 'timed out');

    expect(microtime(true) - $startedAt)->toBeLessThan(2.0);
});
