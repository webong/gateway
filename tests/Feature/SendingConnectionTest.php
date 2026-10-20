<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Email\EmailUpstreamCatalog;
use Webong\Gateway\Email\MailboxConnectionStore;
use Webong\Gateway\Protocol\EventKind;
use Webong\Gateway\Protocol\GatewayAction;
use Webong\Gateway\Protocol\GatewayEvent;
use Webong\Gateway\Protocol\Protocol;
use Illuminate\Http\Client\Request as ClientRequest;
use Illuminate\Support\Facades\Http;

it('configures SMTP-only sending without an IMAP credential or route', function (): void {
    app(EmailUpstreamCatalog::class)->create(['protocol' => 'smtp', 'name' => 'smtp-only',
        'address' => 'smtp.example.test:465', 'server_name' => 'smtp.example.test', 'tls_mode' => 'implicit']);
    $account = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', ['id' => 'zorvia'])
        ->assertCreated()->json();
    $base = 'https://localhost/email/mailbox-accounts/zorvia/sending-connections';
    $created = $this->withToken($account['token'])->postJson($base, [
        'address' => 'support@acme.test', 'smtp_upstream_id' => 'smtp-only',
        'smtp_username' => 'support@acme.test', 'smtp_auth' => 'password', 'smtp_secret' => 'provider-pass',
    ])->assertCreated()->assertJsonMissingPath('smtp_secret')->assertJsonMissingPath('imap_client_password')->json();
    $id = $created['connection']['id'];
    expect($created['connection']['mode'])->toBe('sending')
        ->and(app(MailboxConnectionStore::class)->resolve($id, 'imap', allowDisabled: true))->toBeNull()
        ->and(app(MailboxConnectionStore::class)->resolve($id, 'smtp', allowDisabled: true)['secret'])->toBe('provider-pass');
    $this->withToken($account['token'])->getJson($base.'/'.$id)->assertOk()->assertJsonMissingPath('smtp_client_password');
    $this->withToken('wrong')->getJson($base.'/'.$id)->assertUnauthorized();
    app(MailboxConnectionStore::class)->markHealth('zorvia', $id, true);
    $this->withToken($account['token'])->patchJson($base.'/'.$id, ['enabled' => true])->assertOk();
    $auth = app(ProtocolPlanner::class)->planEvent(new GatewayEvent(id: 'auth-1', protocol: Protocol::SMTP,
        kind: EventKind::AUTHENTICATE, route: $id,
        attributes: ['username' => $id, 'password' => $created['smtp_client_password']]));
    expect($auth->action)->toBe(GatewayAction::ACCEPT)
        ->and($auth->metadata['mailbox_connection_id'])->toBe($id);
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->getJson('/_internal/gateway/mailbox-connections/'.$id.'/smtp')->assertOk()
        ->assertJsonPath('upstream_id', 'smtp-only');
    app(\Webong\Gateway\Email\EmailRuntimePolicyStore::class)->create([
        'connected_smtp_enabled' => true, 'http_submission_enabled' => true,
    ]);
    config()->set('gateway.delivery_admin_url', 'http://spinner.internal:5052');
    Http::fake(fn (ClientRequest $request) => Http::response(['id' => $request['receipt_id']], 202));
    $this->withHeaders(['Authorization' => 'Basic '.base64_encode($id.':'.$created['smtp_client_password']),
        'Idempotency-Key' => 'send-only-1'])->postJson('https://localhost/email/submissions', [
            'mail_from' => 'support@acme.test', 'recipients' => ['client@outside.test'],
            'message_base64' => base64_encode("From: support@acme.test\r\nSubject: reply\r\n\r\nhello"),
        ])->assertAccepted();
    Http::assertSent(fn (ClientRequest $request): bool => $request['mailbox_connection_id'] === $id);
    $rotated = $this->withToken($account['token'])->postJson($base.'/'.$id.'/rotate')->assertOk()->json();
    expect($rotated['smtp_client_password'])->not->toBe($created['smtp_client_password']);
    $old = app(ProtocolPlanner::class)->planEvent(new GatewayEvent(id: 'auth-old', protocol: Protocol::SMTP,
        kind: EventKind::AUTHENTICATE, route: $id,
        attributes: ['username' => $id, 'password' => $created['smtp_client_password']]));
    $new = app(ProtocolPlanner::class)->planEvent(new GatewayEvent(id: 'auth-new', protocol: Protocol::SMTP,
        kind: EventKind::AUTHENTICATE, route: $id,
        attributes: ['username' => $id, 'password' => $rotated['smtp_client_password']]));
    expect($old->action)->toBe(GatewayAction::REJECT)->and($new->action)->toBe(GatewayAction::ACCEPT);
    $this->withToken($account['token'])->putJson($base.'/'.$id, [
        'address' => 'support@acme.test', 'smtp_upstream_id' => 'smtp-only',
        'smtp_username' => 'new-user@acme.test', 'smtp_auth' => 'password', 'smtp_secret' => 'new-provider-pass',
    ])->assertOk()->assertJsonPath('enabled', false);
    expect(app(MailboxConnectionStore::class)->resolve($id, 'smtp', allowDisabled: true)['secret'])->toBe('new-provider-pass');
});
