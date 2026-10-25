<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Email\ConnectedIMAPAuthenticator;
use Webong\Gateway\Email\EmailRuntimePolicyStore;
use Webong\Gateway\Email\EmailUpstreamCatalog;
use Webong\Gateway\Email\MailboxConnectionStore;
use Webong\Gateway\Email\MailboxPollStore;
use Webong\Gateway\Protocols\EventKind;
use Webong\Gateway\Protocols\GatewayAction;
use Webong\Gateway\Protocols\GatewayEvent;
use Webong\Gateway\Protocols\Protocol;

it('manages an IMAP-only account connection without granting SMTP', function (): void {
    app(EmailUpstreamCatalog::class)->create(['protocol' => 'imap', 'name' => 'imap-only',
        'address' => 'imap.example.test:993', 'server_name' => 'imap.example.test']);
    $one = $this->withToken('registry-secret')->postJson('https://localhost/accounts', ['id' => 'one'])
        ->assertCreated()->json();
    $two = $this->withToken('registry-secret')->postJson('https://localhost/accounts', ['id' => 'two'])
        ->assertCreated()->json();
    $base = 'https://localhost/email/mailbox-accounts/one/imap-connections';
    $input = ['address' => 'support@acme.test', 'imap_upstream_id' => 'imap-only',
        'imap_username' => 'support@acme.test', 'imap_auth' => 'password', 'imap_secret' => 'upstream-secret'];
    $created = $this->withToken($one['token'])->postJson($base, $input)->assertCreated()
        ->assertJsonPath('connection.mode', 'imap')
        ->assertJsonMissingPath('smtp_client_password')->assertJsonMissingPath('smtp_upstream_id')->json();
    $id = $created['connection']['id'];
    $password = $created['imap_client_password'];
    $store = app(MailboxConnectionStore::class);
    app(EmailUpstreamCatalog::class)->create(['protocol' => 'smtp', 'name' => 'smtp-optional',
        'address' => 'smtp.example.test:465', 'server_name' => 'smtp.example.test', 'tls_mode' => 'implicit']);
    $sending = $this->withToken($one['token'])->postJson('https://localhost/email/mailbox-accounts/one/sending-connections', [
        'address' => 'support@acme.test', 'smtp_upstream_id' => 'smtp-optional',
        'smtp_username' => 'support@acme.test', 'smtp_auth' => 'password', 'smtp_secret' => 'sending-secret',
    ])->assertCreated()->json();
    expect($sending['connection']['id'])->not->toBe($id);
    expect($store->resolve($id, 'imap', allowDisabled: true)['secret'])->toBe('upstream-secret')
        ->and($store->resolve($id, 'smtp', allowDisabled: true))->toBeNull()
        ->and($store->authenticate($id, $password, 'smtp'))->toBeNull();
    $this->withToken($one['token'])->getJson($base.'/'.$id)->assertOk()
        ->assertJsonMissingPath('imap_client_password')->assertJsonMissingPath('imap_secret');
    $this->withToken($two['token'])->getJson($base.'/'.$id)->assertUnauthorized();
    $this->withToken($two['token'])->getJson('https://localhost/email/mailbox-accounts/two/imap-connections/'.$id)
        ->assertNotFound();
    $this->withToken($one['token'])->getJson('https://localhost/email/mailbox-accounts/one/connections/'.$id)
        ->assertNotFound();
    $this->withToken($one['token'])->getJson('https://localhost/email/mailbox-accounts/one/sending-connections/'.$id)
        ->assertNotFound();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->getJson('/_internal/gateway/mailbox-connections/'.$id.'/smtp')->assertNotFound();
    $this->withToken($one['token'])->patchJson($base.'/'.$id, ['enabled' => true])->assertConflict();
    $this->withToken($one['token'])->postJson($base.'/'.$id.'/test')->assertOk()
        ->assertJsonPath('health', 'unhealthy');

    // A successful provider probe is represented by the same observation used by
    // the real /test endpoint; no SMTP observation is required for this mode.
    $store->markHealth('one', $id, true);
    $this->withToken($one['token'])->patchJson($base.'/'.$id, ['enabled' => true])->assertOk()
        ->assertJsonPath('enabled', true);
    $imapGrant = app(ConnectedIMAPAuthenticator::class)->authorize(new GatewayEvent(
        id: 'imap-only-auth', protocol: Protocol::IMAP, kind: EventKind::AUTHENTICATE,
        route: $id, attributes: ['username' => $id, 'password' => $password],
    ));
    expect($imapGrant?->upstreamId)->toBe('imap-only')
        ->and($store->authenticate($id, $password, 'smtp'))->toBeNull();
    $smtpAuth = app(ProtocolPlanner::class)->planEvent(new GatewayEvent(
        id: 'smtp-denied', protocol: Protocol::SMTP, kind: EventKind::AUTHENTICATE,
        route: $id, attributes: ['username' => $id, 'password' => $password],
    ));
    expect($smtpAuth->action)->toBe(GatewayAction::REJECT);
    app(EmailRuntimePolicyStore::class)->create(['http_submission_enabled' => true, 'connected_smtp_enabled' => true]);
    $this->withHeaders(['Authorization' => 'Basic '.base64_encode($id.':'.$password),
        'Idempotency-Key' => 'imap-must-not-send'])->postJson('https://localhost/email/submissions', [
            'mail_from' => 'support@acme.test', 'recipients' => ['outside@example.test'],
            'message_base64' => base64_encode("From: support@acme.test\r\n\r\nhello"),
        ])->assertUnauthorized();

    $pollUrl = $base.'/'.$id.'/polling';
    $this->withToken($one['token'])->putJson($pollUrl, ['callback_url' => 'https://zorvia.example.test/mail',
        'webhook_secret' => str_repeat('a', 64), 'enabled' => true])->assertCreated()
        ->assertJsonPath('connection_id', $id);
    $poll = app(MailboxPollStore::class)->find('one', $id);
    expect(app(MailboxPollStore::class)->claim($poll->id)['connection_id'])->toBe($id);

    $rotated = $this->withToken($one['token'])->postJson($base.'/'.$id.'/rotate')->assertOk()->json();
    expect($store->authenticate($id, $password, 'imap'))->toBeNull()
        ->and($store->authenticate($id, $rotated['imap_client_password'], 'imap'))->not->toBeNull();
    $this->withToken($one['token'])->putJson($base.'/'.$id, [...$input, 'imap_secret' => 'new-secret'])
        ->assertOk()->assertJsonPath('enabled', false)->assertJsonPath('health', 'untested');
    expect($store->resolve($id, 'imap', allowDisabled: true)['secret'])->toBe('new-secret')
        ->and($store->resolve($id, 'smtp', allowDisabled: true))->toBeNull();
    $this->withToken($one['token'])->deleteJson($base.'/'.$id)->assertOk();
    expect(app(MailboxPollStore::class)->find('one', $id))->toBeNull();
});
