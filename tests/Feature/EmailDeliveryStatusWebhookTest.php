<?php

declare(strict_types=1);

use Illuminate\Http\Client\Request as ClientRequest;
use Illuminate\Support\Facades\Crypt;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Http;
use Webong\Gateway\Email\EmailSubmissionAuthenticator;
use Webong\Gateway\Email\EmailSubmissionGrant;
use Webong\Gateway\Protocols\GatewayEvent;

final class DeliveryStatusEmailSubmissionAuthenticator implements EmailSubmissionAuthenticator
{
    public function authorize(GatewayEvent $event): ?EmailSubmissionGrant
    {
        return ($event->attributes['username'] ?? null) === 'status-client'
            && ($event->attributes['password'] ?? null) === 'secret'
            ? new EmailSubmissionGrant('status-account', ['sender@example.test'], dailyLimit: 10)
            : null;
    }
}

function statusWebhookSubmissionBody(): array
{
    return [
        'mail_from' => 'sender@example.test',
        'recipients' => ['recipient@example.test'],
        'message_base64' => base64_encode("From: sender@example.test\r\nSubject: status\r\n\r\nhello"),
    ];
}

function createStatusWebhookAccount(mixed $case): string
{
    return $case->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', [
        'id' => 'status-account',
    ])->assertCreated()->json('token');
}

beforeEach(function (): void {
    config()->set('gateway.email.submission_authenticator', DeliveryStatusEmailSubmissionAuthenticator::class);
    app(\Webong\Gateway\Email\EmailRuntimePolicyStore::class)->create(['http_submission_enabled' => true]);
    config()->set('gateway.delivery_admin_url', 'http://spinner.internal:5052');
});

it('configures one account-scoped delivery status hook without exposing its secret', function (): void {
    $token = createStatusWebhookAccount($this);
    $secret = str_repeat('ab', 32);
    $url = 'https://localhost/email/mailbox-accounts/status-account/delivery-status-hook';

    $created = $this->withToken($token)->postJson($url, [
        'callback_url' => 'https://consumer.example.test/gateway/email-status',
        'network_scope' => 'public',
        'webhook_secret' => $secret,
        'enabled' => true,
    ])->assertCreated()->assertJsonPath('account_id', 'status-account')
        ->assertJsonPath('callback_url', 'https://consumer.example.test/gateway/email-status')
        ->assertJsonPath('network_scope', 'public')->assertJsonPath('enabled', true)
        ->assertJsonPath('secret_configured', true)->assertJsonMissingPath('webhook_secret')->json();

    $other = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', [
        'id' => 'other-account',
    ])->assertCreated()->json('token');
    $this->withToken($other)->getJson($url)->assertUnauthorized();

    expect(DB::table('configuration_secrets')->where('resource_id', $created['id'])->value('ciphertext'))
        ->not->toContain($secret)
        ->and(Crypt::decryptString(DB::table('configuration_secrets')
            ->where('resource_id', $created['id'])->value('ciphertext')))->toBe($secret);

    $this->withToken($token)->postJson($url, [
        'callback_url' => 'https://consumer.example.test/other',
        'network_scope' => 'public',
        'webhook_secret' => str_repeat('cd', 32),
        'enabled' => true,
    ])->assertConflict();
    $this->withToken($token)->getJson($url)->assertOk()->assertJsonPath('id', $created['id'])
        ->assertJsonMissingPath('webhook_secret');
    $this->withToken($token)->patchJson($url, ['enabled' => false])
        ->assertOk()->assertJsonPath('enabled', false);
    $this->withToken($token)->deleteJson($url)->assertOk()->assertJsonPath('deleted', true);
    $this->withToken($token)->getJson($url)->assertNotFound();
});

it('retries a signed terminal delivery event with the same event identity', function (): void {
    $token = createStatusWebhookAccount($this);
    $secret = str_repeat('12', 32);
    $hook = $this->withToken($token)->postJson(
        'https://localhost/email/mailbox-accounts/status-account/delivery-status-hook',
        ['callback_url' => 'https://consumer.example.test/gateway/email-status',
            'network_scope' => 'public', 'webhook_secret' => $secret, 'enabled' => true],
    )->assertCreated()->json();

    $callbacks = [];
    Http::fake(function (ClientRequest $request) use (&$callbacks) {
        if ($request->url() === 'http://spinner.internal:5052/email/submissions') {
            return Http::response(['id' => $request['receipt_id'], 'state' => 'queued'], 202);
        }
        if (str_starts_with($request->url(), 'http://spinner.internal:5052/deliveries/')) {
            $id = basename(parse_url($request->url(), PHP_URL_PATH));
            return Http::response(['id' => $id, 'state' => 'delivered', 'attempts' => 2,
                'updated_at' => '2026-10-23T10:15:30Z'], 200);
        }
        if ($request->url() === 'https://consumer.example.test/gateway/email-status') {
            $callbacks[] = $request;
            return Http::response([], count($callbacks) === 1 ? 503 : 204);
        }

        return Http::response([], 404);
    });

    $delivery = $this->withHeaders([
        'Authorization' => 'Basic '.base64_encode('status-client:secret'),
        'Idempotency-Key' => 'status-event-1',
    ])->postJson('https://localhost/email/submissions', statusWebhookSubmissionBody())
        ->assertAccepted()->json('id');

    $this->artisan('gateway:send-email-delivery-status-events')->assertSuccessful();
    $event = DB::table('email_delivery_status_events')->where('delivery_id', $delivery)->first();
    expect($event->state)->toBe('pending')->and($event->callback_attempts)->toBe(1);
    DB::table('email_delivery_status_events')->where('delivery_id', $delivery)
        ->update(['next_attempt_at' => now('UTC')->subSecond()]);
    $this->artisan('gateway:send-email-delivery-status-events')->assertSuccessful();

    expect($callbacks)->toHaveCount(2);
    $first = $callbacks[0];
    $second = $callbacks[1];
    $payload = json_decode($first->body(), true, 512, JSON_THROW_ON_ERROR);
    expect($payload)->toMatchArray([
        'event' => 'email.delivery.status',
        'delivery_id' => $delivery,
        'state' => 'delivered',
        'attempts' => 2,
        'occurred_at' => '2026-10-23T10:15:30Z',
        'reason_code' => 'accepted',
    ])->and($payload['event_id'])->toMatch('/\A[a-f0-9]{64}\z/')
        ->and($second->body())->toBe($first->body())
        ->and($second->header('X-Gateway-Hook-Id'))->toBe([$hook['id']])
        ->and($second->header('X-Gateway-Event-Id'))->toBe([$payload['event_id']])
        ->and($second->header('Idempotency-Key'))->toBe(['gateway-email-delivery-'.$payload['event_id']]);
    $canonical = "v1\n{$hook['id']}\n{$payload['event_id']}\n".hash('sha256', $first->body());
    expect($first->header('X-Gateway-Signature'))->toBe([
        'v1='.hash_hmac('sha256', $canonical, hex2bin($secret)),
    ])->and($second->header('X-Gateway-Signature'))->toBe($first->header('X-Gateway-Signature'))
        ->and(DB::table('email_delivery_status_events')->where('delivery_id', $delivery)->value('state'))
        ->toBe('sent');
});

it('emits a provider-neutral dead event without leaking the delivery error', function (): void {
    $token = createStatusWebhookAccount($this);
    $this->withToken($token)->postJson(
        'https://localhost/email/mailbox-accounts/status-account/delivery-status-hook',
        ['callback_url' => 'https://consumer.example.test/gateway/email-status',
            'network_scope' => 'public', 'webhook_secret' => str_repeat('34', 32), 'enabled' => true],
    )->assertCreated();

    $callback = null;
    Http::fake(function (ClientRequest $request) use (&$callback) {
        if ($request->url() === 'http://spinner.internal:5052/email/submissions') {
            return Http::response(['id' => $request['receipt_id'], 'state' => 'queued'], 202);
        }
        if (str_starts_with($request->url(), 'http://spinner.internal:5052/deliveries/')) {
            $id = basename(parse_url($request->url(), PHP_URL_PATH));
            return Http::response(['id' => $id, 'state' => 'failed', 'attempts' => 1,
                'body_assertion' => 'acceptance_unknown',
                'error' => '550 provider-specific secret diagnostic',
                'updated_at' => '2026-10-23T11:00:00Z'], 200);
        }
        $callback = $request;
        return Http::response([], 204);
    });

    $this->withHeaders([
        'Authorization' => 'Basic '.base64_encode('status-client:secret'),
        'Idempotency-Key' => 'dead-event-1',
    ])->postJson('https://localhost/email/submissions', statusWebhookSubmissionBody())->assertAccepted();
    $this->artisan('gateway:send-email-delivery-status-events')->assertSuccessful();

    expect($callback)->not->toBeNull();
    $payload = json_decode($callback->body(), true, 512, JSON_THROW_ON_ERROR);
    expect($payload['state'])->toBe('dead')
        ->and($payload['reason_code'])->toBe('acceptance_unknown')
        ->and($callback->body())->not->toContain('provider-specific', '550', 'secret diagnostic');
});

it('uses a new receipt when a client retries a terminal submission with a new idempotency key', function (): void {
    Http::fake(function (ClientRequest $request) {
        if ($request->method() === 'POST') {
            return Http::response(['id' => $request['receipt_id'], 'state' => 'queued'], 202);
        }
        $id = basename(parse_url($request->url(), PHP_URL_PATH));
        return Http::response(['id' => $id, 'state' => 'failed', 'attempts' => 3], 200);
    });

    $authorization = 'Basic '.base64_encode('status-client:secret');
    $first = $this->withHeaders(['Authorization' => $authorization, 'Idempotency-Key' => 'terminal-1'])
        ->postJson('https://localhost/email/submissions', statusWebhookSubmissionBody())
        ->assertAccepted()->json('id');
    $this->withHeaders(['Authorization' => $authorization])
        ->getJson('https://localhost/email/submissions/'.$first)->assertOk()->assertJsonPath('state', 'rejected');
    $second = $this->withHeaders(['Authorization' => $authorization, 'Idempotency-Key' => 'terminal-2'])
        ->postJson('https://localhost/email/submissions', statusWebhookSubmissionBody())
        ->assertAccepted()->json('id');

    expect($second)->not->toBe($first);
});
