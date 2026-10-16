<?php

declare(strict_types=1);

use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Crypt;
use Illuminate\Support\Facades\Http;
use Webong\Gateway\Email\ConnectedIMAPAuthenticator;
use Webong\Gateway\Email\ConnectedSMTPAuthenticator;
use Webong\Gateway\Email\EmailUpstreamCatalog;
use Webong\Gateway\Email\MailboxConnectionStore;
use Webong\Gateway\Email\ProviderMailboxOAuthRefresher;
use Webong\Gateway\Protocol\EventKind;
use Webong\Gateway\Protocol\GatewayEvent;
use Webong\Gateway\Protocol\Protocol;

function mailboxInput(string $address): array
{
    return [
        'address' => $address, 'imap_upstream_id' => 'imap-a', 'smtp_upstream_id' => 'smtp-a',
        'imap_username' => $address, 'smtp_username' => $address,
        'imap_auth' => 'password', 'smtp_auth' => 'password',
        'imap_secret' => 'in-secret', 'smtp_secret' => 'out-secret',
    ];
}

function registerMailboxUpstreams(): void
{
    config()->set('gateway.email.upstream_allowed_hosts', ['imap.test', 'smtp.test']);
    $catalog = app(EmailUpstreamCatalog::class);
    $catalog->create(['protocol' => 'imap', 'name' => 'imap-a',
        'address' => 'imap.test:993', 'server_name' => 'imap.test']);
    $catalog->create(['protocol' => 'smtp', 'name' => 'smtp-a',
        'address' => 'smtp.test:465', 'server_name' => 'smtp.test', 'tls_mode' => 'implicit']);
}

it('keeps mailbox credentials and control scoped to the owning account', function (): void {
    registerMailboxUpstreams();
    $store = app(MailboxConnectionStore::class);
    $aToken = $store->createAccount('account-a');
    $bToken = $store->createAccount('account-b');
    $created = $store->create('account-a', mailboxInput('a@example.test'));
    $id = $created['record']->id;

    expect($store->authorized('account-a', $aToken))->toBeTrue()
        ->and($store->authorized('account-b', $aToken))->toBeFalse()
        ->and($store->find('account-b', $id))->toBeNull()
        ->and($store->delete('account-b', $id))->toBeFalse()
        ->and(DB::table('mailbox_connections')->where('id', $id)->value('imap_secret'))->not->toContain('in-secret')
        ->and(DB::table('mailbox_connections')->where('id', $id)->value('smtp_secret'))->not->toContain('out-secret');

    $store->markHealth('account-a', $id, true);
    $store->update('account-a', $id, true);
    $this->getJson('/_internal/gateway/mailbox-connections/'.$id.'/smtp')->assertNotFound();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->getJson('/_internal/gateway/mailbox-connections/'.$id.'/smtp')->assertOk()
        ->assertJsonPath('upstream_address', 'smtp.test:465')
        ->assertJsonPath('upstream_server_name', 'smtp.test')
        ->assertJsonPath('upstream_tls_mode', 'implicit');
    expect($store->authenticate($id, $created['imap_password'], 'imap'))->not->toBeNull()
        ->and($store->authenticate($id, $created['smtp_password'], 'smtp'))->not->toBeNull()
        ->and($store->authenticate($id, $created['imap_password'], 'smtp'))->toBeNull();

    $imapGrant = app(ConnectedIMAPAuthenticator::class)->authorize(new GatewayEvent(
        id: 'imap-auth', protocol: Protocol::IMAP, kind: EventKind::AUTHENTICATE,
        route: $id, attributes: ['username' => $id, 'password' => $created['imap_password']],
    ));
    $smtpGrant = app(ConnectedSMTPAuthenticator::class)->authorize(new GatewayEvent(
        id: 'smtp-auth', protocol: Protocol::SMTP, kind: EventKind::AUTHENTICATE,
        route: $id, attributes: ['username' => $id, 'password' => $created['smtp_password']],
    ));
    expect($imapGrant?->upstreamId)->toBe('imap-a')
        ->and($smtpGrant?->connectionId)->toBe($id);

    $replaced = $store->replace('account-a', $id, mailboxInput('new@example.test'));
    expect($replaced->enabled)->toBeFalsy()->and($replaced->health)->toBe('untested')
        ->and($store->resolve($id, 'smtp'))->toBeNull()
        ->and($store->resolve($id, 'smtp', allowDisabled: true)['address'])->toBe('new@example.test')
        ->and($store->authorized('account-b', $bToken))->toBeTrue();
});

it('exposes an HTTPS-only account API without leaking provider secrets', function (): void {
    registerMailboxUpstreams();
    $this->withToken('registry-secret')->postJson('/email/mailbox-accounts', ['id' => 'one'])->assertForbidden();
    $one = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', ['id' => 'one'])->assertCreated()->json();
    $two = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', ['id' => 'two'])->assertCreated()->json();
    $created = $this->withToken($one['token'])->postJson('https://localhost/email/mailbox-accounts/one/connections', mailboxInput('a@example.test'))
        ->assertCreated()->json();
    $id = $created['connection']['id'];
    expect($created)->toHaveKeys(['imap_client_password', 'smtp_client_password'])
        ->and(json_encode($created))->not->toContain('in-secret', 'out-secret');
    $this->withToken($two['token'])->getJson('https://localhost/email/mailbox-accounts/one/connections/'.$id)->assertUnauthorized();
    $this->withToken($two['token'])->getJson('https://localhost/email/mailbox-accounts/two/connections/'.$id)->assertNotFound();
    $this->withToken($one['token'])->getJson('https://localhost/email/mailbox-accounts/one/connections/'.$id)
        ->assertOk()->assertDontSee('in-secret')->assertDontSee('out-secret');
    $this->withToken($one['token'])->patchJson('https://localhost/email/mailbox-accounts/one/connections/'.$id, ['enabled' => true])
        ->assertConflict();
    $this->withToken($one['token'])->putJson('https://localhost/email/mailbox-accounts/one/connections/'.$id, mailboxInput('b@example.test'))
        ->assertOk()->assertJsonPath('enabled', false);
});

it('refreshes Google and Microsoft tokens for the requested transport and stores rotations', function (): void {
    config()->set('gateway.email.oauth.google', ['client_id' => 'google-id', 'client_secret' => 'google-secret']);
    config()->set('gateway.email.oauth.microsoft', ['client_id' => 'ms-id', 'client_secret' => 'ms-secret', 'tenant' => 'common']);
    Http::fake([
        'oauth2.googleapis.com/token' => Http::response(['access_token' => 'google-access', 'refresh_token' => 'google-rotated']),
        'login.microsoftonline.com/common/oauth2/v2.0/token' => Http::response(['access_token' => 'ms-access', 'refresh_token' => 'ms-rotated']),
    ]);
    $refresher = new ProviderMailboxOAuthRefresher();
    expect($refresher->refresh('google', 'imap', 'google-refresh')['access_token'])->toBe('google-access')
        ->and($refresher->refresh('microsoft', 'smtp', 'ms-refresh')['refresh_token'])->toBe('ms-rotated');
    Http::assertSent(fn ($request): bool => $request->url() === 'https://login.microsoftonline.com/common/oauth2/v2.0/token'
        && str_contains($request->body(), 'scope=https%3A%2F%2Foutlook.office.com%2FSMTP.Send'));

    registerMailboxUpstreams();
    $store = new MailboxConnectionStore($refresher, app(EmailUpstreamCatalog::class));
    $store->createAccount('oauth-account');
    $input = mailboxInput('oauth@example.test');
    $input['imap_auth'] = 'google';
    $input['smtp_auth'] = 'microsoft';
    $input['imap_secret'] = 'google-refresh';
    $input['smtp_secret'] = 'ms-refresh';
    $id = $store->create('oauth-account', $input)['record']->id;
    expect($store->resolve($id, 'imap', allowDisabled: true)['secret'])->toBe('google-access')
        ->and($store->resolve($id, 'smtp', allowDisabled: true)['secret'])->toBe('ms-access')
        ->and(Crypt::decryptString(DB::table('mailbox_connections')->where('id', $id)->value('imap_secret')))->toBe('google-rotated')
        ->and(Crypt::decryptString(DB::table('mailbox_connections')->where('id', $id)->value('smtp_secret')))->toBe('ms-rotated');
    Http::assertSent(fn ($request): bool => $request->url() === 'https://oauth2.googleapis.com/token'
        && str_contains($request->body(), 'refresh_token=google-refresh'));
});
