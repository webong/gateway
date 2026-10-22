<?php

declare(strict_types=1);

use Webong\Gateway\WebhookScript\WebhookScriptCredentialExecutor;
use Webong\Gateway\WebhookScript\WebhookScriptRuntime;

it('turns credential variables into one declarative request', function (): void {
    $executor = new WebhookScriptCredentialExecutor(new WebhookScriptRuntime);
    $delivery = $executor->execute(<<<'PRIMI'
        forward(var('credential.source_url'), json_encode([
            'context': ['account_id': var('credential.account_id'), 'protocol': var('credential.protocol')]
        ]), 'PATCH', ['Content-Type: application/json']);
        PRIMI, [
            'source_url' => 'https://application.example.test/setup',
            'account_id' => 'account-one',
            'connection_id' => 'connection-one',
            'protocol' => 'imap',
            'address' => 'mail@example.test',
        ]);

    expect($delivery->url)->toBe('https://application.example.test/setup')
        ->and($delivery->method)->toBe('PATCH')
        ->and(json_decode($delivery->body, true))->toBe([
            'context' => ['account_id' => 'account-one', 'protocol' => 'imap'],
        ]);
});
