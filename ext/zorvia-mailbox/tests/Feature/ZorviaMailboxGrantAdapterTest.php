<?php

declare(strict_types=1);

use Webong\Gateway\Accounts\AccountStore;
use Webong\Gateway\Email\ApplicationMailboxGrantSource;
use Webong\Gateway\ZorviaMailbox\ZorviaApplicationMailboxGrantSource;
use Webong\Gateway\ZorviaMailbox\ZorviaMailboxGrantTransport;

it('registers zorvia and translates the neutral grant request for PATCH and PUT', function (): void {
    expect(config('gateway.email.credential_source_adapters.zorvia'))
        ->toBe(ZorviaApplicationMailboxGrantSource::class);
    $accountToken = app(AccountStore::class)->create('customer-42');
    $sourceToken = str_repeat('z', 48);
    $source = $this->withToken($accountToken)
        ->postJson('https://localhost/email/mailbox-accounts/customer-42/credential-sources', [
            'name' => 'zorvia-primary', 'adapter' => 'zorvia',
            'url' => 'https://app.example.com/api/connect/connections/setup',
            'authorization' => $sourceToken,
        ])->assertCreated()->assertJsonPath('adapter', 'zorvia')
        ->assertDontSee($sourceToken)->json('id');

    $transport = new class implements ZorviaMailboxGrantTransport {
        public array $calls = [];

        public function exchange(string $url, string $method, array $headers, string $body): array
        {
            $this->calls[] = compact('url', 'method', 'headers', 'body');
            $context = json_decode($body, true, 8, JSON_THROW_ON_ERROR)['context'];
            return ['status' => 200, 'body' => json_encode([
                'account_id' => $context['account_id'], 'connection_id' => $context['connection_id'],
                'address' => $context['address'], 'access_token' => 'short-lived-token',
                'expires_at' => time() + 600, 'capabilities' => ['imap', 'smtp'],
            ], JSON_THROW_ON_ERROR)];
        }
    };
    app()->instance(ZorviaMailboxGrantTransport::class, $transport);
    $resolver = app(ApplicationMailboxGrantSource::class);

    foreach (['PATCH', 'PUT'] as $method) {
        config()->set('gateway-zorvia-mailbox.method', $method);
        $grant = $resolver->resolve($source, 'customer-42', 'connection-7', 'imap', 'mail@example.com');
        expect($grant['access_token'])->toBe('short-lived-token');
    }
    expect($transport->calls)->toBe([
        ['url' => 'https://app.example.com/api/connect/connections/setup', 'method' => 'PATCH',
            'headers' => ['Authorization: Bearer '.$sourceToken, 'Content-Type: application/json',
                'Accept: application/json'],
            'body' => '{"context":{"account_id":"customer-42","connection_id":"connection-7","protocol":"imap","address":"mail@example.com"}}'],
        ['url' => 'https://app.example.com/api/connect/connections/setup', 'method' => 'PUT',
            'headers' => ['Authorization: Bearer '.$sourceToken, 'Content-Type: application/json',
                'Accept: application/json'],
            'body' => '{"context":{"account_id":"customer-42","connection_id":"connection-7","protocol":"imap","address":"mail@example.com"}}'],
    ]);
});

it('contains remote failures and response bodies', function (): void {
    $accountToken = app(AccountStore::class)->create('customer-42');
    $source = $this->withToken($accountToken)
        ->postJson('https://localhost/email/mailbox-accounts/customer-42/credential-sources', [
            'name' => 'zorvia-primary', 'adapter' => 'zorvia',
            'url' => 'https://app.example.com/api/connect/connections/setup',
            'authorization' => str_repeat('z', 48),
        ])->assertCreated()->json('id');
    $transport = new class implements ZorviaMailboxGrantTransport {
        public array $response = ['status' => 401, 'body' => 'remote body containing a secret'];

        public function exchange(string $url, string $method, array $headers, string $body): array
        {
            return $this->response;
        }
    };
    app()->instance(ZorviaMailboxGrantTransport::class, $transport);
    $resolver = app(ApplicationMailboxGrantSource::class);
    foreach ([
        ['status' => 401, 'body' => 'remote body containing a secret'],
        ['status' => 200, 'body' => '{invalid'],
        ['status' => 200, 'body' => str_repeat('x', 16385)],
    ] as $response) {
        $transport->response = $response;
        try { $resolver->resolve($source, 'customer-42', 'connection-7', 'smtp', 'mail@example.com'); }
        catch (RuntimeException $error) {
            expect($error->getMessage())->toBe('Mailbox credential source is unavailable.')
                ->not->toContain('remote body containing a secret');
            continue;
        }
        test()->fail('The invalid Zorvia response must be rejected.');
    }
    config()->set('gateway-zorvia-mailbox.method', 'POST');
    expect(fn () => $resolver->resolve($source, 'customer-42', 'connection-7', 'smtp', 'mail@example.com'))
        ->toThrow(RuntimeException::class, 'Mailbox credential source is unavailable.');
});
