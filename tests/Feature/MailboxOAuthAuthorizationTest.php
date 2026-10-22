<?php

declare(strict_types=1);

use Illuminate\Support\Facades\Crypt;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Http;
use Webong\Gateway\Accounts\AccountStore;
use Webong\Gateway\Email\EmailUpstreamCatalog;
use Webong\Gateway\Email\MailboxConnectionTester;
use Webong\Gateway\Email\MailboxConnectionStore;

function oauthFixtures(): array
{
    config()->set('gateway.email.oauth.callback_url', 'https://zorvia.example.test/mail/oauth/callback');
    config()->set('gateway.email.oauth.google', ['client_id' => 'google-id', 'client_secret' => 'google-secret',
        'imap_upstream_id' => 'google-imap', 'smtp_upstream_id' => 'google-smtp']);
    config()->set('gateway.email.oauth.microsoft', ['client_id' => 'microsoft-id', 'client_secret' => 'microsoft-secret',
        'tenant' => 'common', 'imap_upstream_id' => 'microsoft-imap', 'smtp_upstream_id' => 'microsoft-smtp']);
    $catalog = app(EmailUpstreamCatalog::class);
    foreach (['google', 'microsoft'] as $provider) {
        $catalog->create(['protocol' => 'imap', 'name' => $provider.'-imap',
            'address' => 'imap.'.$provider.'.test:993', 'server_name' => 'imap.'.$provider.'.test']);
        $catalog->create(['protocol' => 'smtp', 'name' => $provider.'-smtp',
            'address' => 'smtp.'.$provider.'.test:465', 'server_name' => 'smtp.'.$provider.'.test', 'tls_mode' => 'implicit']);
    }
    $accounts = app(AccountStore::class);
    $token = $accounts->create('oauth-one');
    $other = $accounts->create('oauth-two');
    return [$token, $other];
}

it('starts provider-neutral Google and Microsoft flows with account-bound PKCE and exact scopes', function (): void {
    [$token, $other] = oauthFixtures();
    $base = 'https://localhost/email/mailbox-accounts/oauth-one/oauth';
    $this->withToken($other)->postJson($base.'/start', [
        'provider' => 'google', 'mode' => 'imap', 'sending' => false, 'address' => 'user@gmail.com',
    ])->assertUnauthorized();
    $google = $this->withToken($token)->postJson($base.'/start', [
        'provider' => 'google', 'mode' => 'imap', 'sending' => false, 'address' => 'user@gmail.com',
    ])->assertCreated()->json();
    parse_str(parse_url($google['authorization_url'], PHP_URL_QUERY), $query);
    expect($query['scope'])->toBe('https://mail.google.com/')
        ->and($query['access_type'])->toBe('offline')
        ->and($query['code_challenge_method'])->toBe('S256')
        ->and($query['redirect_uri'])->toBe('https://zorvia.example.test/mail/oauth/callback')
        ->and($query['state'])->toBe($google['state']);
    $stored = DB::table('mailbox_oauth_transactions')->first();
    expect($stored->ciphertext)->not->toContain('google-secret', $google['state']);
    $doc = json_decode(Crypt::decryptString($stored->ciphertext), true);
    $challenge = rtrim(strtr(base64_encode(hash('sha256', $doc['verifier'], true)), '+/', '-_'), '=');
    expect($query['code_challenge'])->toBe($challenge);
    $microsoft = $this->withToken($token)->postJson($base.'/start', [
        'provider' => 'microsoft', 'mode' => 'mailbox', 'sending' => true, 'address' => 'user@example.com',
    ])->assertCreated()->json();
    parse_str(parse_url($microsoft['authorization_url'], PHP_URL_QUERY), $query);
    expect($query['scope'])->toContain('offline_access', 'IMAP.AccessAsUser.All', 'SMTP.Send');
    $receiveOnly = $this->withToken($token)->postJson($base.'/start', [
        'provider' => 'microsoft', 'mode' => 'imap', 'sending' => false, 'address' => 'user@example.com',
    ])->assertCreated()->json();
    parse_str(parse_url($receiveOnly['authorization_url'], PHP_URL_QUERY), $query);
    expect($query['scope'])->toContain('offline_access', 'IMAP.AccessAsUser.All')
        ->not->toContain('SMTP.Send');
    $this->withToken($token)->postJson('http://localhost/email/mailbox-accounts/oauth-one/oauth/start', [
        'provider' => 'google', 'mode' => 'imap', 'sending' => false, 'address' => 'user@gmail.com',
    ])->assertForbidden();
    $this->withToken($token)->postJson($base.'/start', [
        'provider' => 'microsoft', 'mode' => 'imap', 'sending' => true, 'address' => 'user@example.com',
    ])->assertUnprocessable();
});

it('exchanges once, stores only encrypted refresh credentials, and rejects mismatched callbacks', function (): void {
    [$token, $other] = oauthFixtures();
    $tester = Mockery::mock(MailboxConnectionTester::class);
    $tester->shouldReceive('test')->once()->andReturn(true);
    app()->instance(MailboxConnectionTester::class, $tester);
    Http::fake([
        'oauth2.googleapis.com/token' => Http::response([
            'access_token' => 'access-sensitive', 'refresh_token' => 'refresh-sensitive',
        ]),
        'gmail.googleapis.com/gmail/v1/users/me/profile' => Http::response(['emailAddress' => 'user@gmail.com']),
    ]);
    $base = 'https://localhost/email/mailbox-accounts/oauth-one/oauth';
    $start = $this->withToken($token)->postJson($base.'/start', [
        'provider' => 'google', 'mode' => 'imap', 'sending' => false, 'address' => 'user@gmail.com',
    ])->assertCreated()->json();
    $callback = ['provider' => 'google', 'state' => $start['state'], 'code' => 'provider-code',
        'callback_url' => 'https://zorvia.example.test/mail/oauth/callback'];
    $this->withToken($other)->postJson('https://localhost/email/mailbox-accounts/oauth-two/oauth/complete', $callback)
        ->assertStatus(409);
    $this->withToken($token)->postJson($base.'/complete', [...$callback, 'provider' => 'microsoft'])
        ->assertStatus(409);
    $this->withToken($token)->postJson($base.'/complete', [...$callback, 'callback_url' => 'https://wrong.test/callback'])
        ->assertStatus(409);
    $complete = $this->withToken($token)->postJson($base.'/complete', $callback)->assertOk()->json();
    expect($complete['connection']['enabled'])->toBeTrue()
        ->and($complete['connection']['imap_auth'])->toBe('google')
        ->and($complete['imap_client_password'])->toBeString()
        ->and(json_encode($complete))->not->toContain('refresh-sensitive', 'access-sensitive', 'google-secret');
    $replay = $this->withToken($token)->postJson($base.'/complete', $callback)->assertOk()->json();
    expect($replay)->toBe($complete);
    Http::assertSentCount(2);
    $this->withToken($token)->postJson($base.'/complete', [...$callback, 'code' => 'other-code'])->assertStatus(409);
    $id = $complete['connection']['id'];
    expect(DB::table('configuration_secrets')->where('resource_id', $id)->value('ciphertext'))
        ->not->toContain('refresh-sensitive');
    expect(Crypt::decryptString(DB::table('configuration_secrets')->where('resource_id', $id)->value('ciphertext')))
        ->toContain('refresh-sensitive');
    Http::assertSent(fn ($request): bool => str_contains($request->body(), 'code_verifier=')
        && str_contains($request->body(), 'redirect_uri=https%3A%2F%2Fzorvia.example.test'));
});

it('expires state and redacts provider failures', function (): void {
    [$token] = oauthFixtures();
    Http::fake(['login.microsoftonline.com/*' => Http::response(['error_description' => 'secret provider body'], 400)]);
    $base = 'https://localhost/email/mailbox-accounts/oauth-one/oauth';
    $input = ['provider' => 'microsoft', 'mode' => 'imap', 'sending' => false, 'address' => 'user@example.com'];
    $start = $this->withToken($token)->postJson($base.'/start', $input)->assertCreated()->json();
    $callback = ['provider' => 'microsoft', 'state' => $start['state'], 'code' => 'provider-code',
        'callback_url' => 'https://zorvia.example.test/mail/oauth/callback'];
    $response = $this->withToken($token)->postJson($base.'/complete', $callback)->assertStatus(409);
    expect($response->getContent())->not->toContain('secret provider body');
    $expired = $this->withToken($token)->postJson($base.'/start', $input)->assertCreated()->json();
    DB::table('mailbox_oauth_transactions')->where('state_hash', hash('sha256', $expired['state']))
        ->update(['expires_at' => now('UTC')->subSecond()]);
    $this->withToken($token)->postJson($base.'/complete', [...$callback, 'state' => $expired['state']])->assertStatus(409);
});

it('completes Microsoft mailbox mode and rolls back a failed replacement', function (): void {
    [$token] = oauthFixtures();
    $tester = Mockery::mock(MailboxConnectionTester::class);
    $tester->shouldReceive('test')->twice()->andReturn(true, false);
    app()->instance(MailboxConnectionTester::class, $tester);
    Http::fake(function ($request) {
        parse_str($request->body(), $body);
        return Http::response(['access_token' => 'microsoft-access',
            'refresh_token' => ($body['grant_type'] ?? null) === 'refresh_token'
                ? 'microsoft-rotated' : 'microsoft-refresh']);
    });
    $base = 'https://localhost/email/mailbox-accounts/oauth-one/oauth';
    $startInput = ['provider' => 'microsoft', 'mode' => 'mailbox', 'sending' => true,
        'address' => 'person@example.com'];
    $first = $this->withToken($token)->postJson($base.'/start', $startInput)->assertCreated()->json();
    $callback = ['provider' => 'microsoft', 'state' => $first['state'], 'code' => 'ms-code',
        'callback_url' => 'https://zorvia.example.test/mail/oauth/callback'];
    $created = $this->withToken($token)->postJson($base.'/complete', $callback)->assertOk()->json();
    expect($created['connection']['enabled'])->toBeTrue()
        ->and($created['connection']['imap_auth'])->toBe('microsoft')
        ->and($created['connection']['smtp_auth'])->toBe('microsoft')
        ->and($created['smtp_client_password'])->toBeString();
    $id = $created['connection']['id'];
    app(MailboxConnectionStore::class)->resolve($id, 'imap');
    $secrets = json_decode(Crypt::decryptString(DB::table('configuration_secrets')
        ->where('resource_id', $id)->value('ciphertext')), true);
    expect($secrets['imap_secret'])->toBe('microsoft-rotated')
        ->and($secrets['smtp_secret'])->toBe('microsoft-rotated');
    $before = DB::table('configuration_secrets')->where('resource_id', $id)->value('ciphertext');
    $replacement = $this->withToken($token)->postJson($base.'/start', [
        ...$startInput, 'connection_id' => $id,
    ])->assertCreated()->json();
    $this->withToken($token)->postJson($base.'/complete', [
        ...$callback, 'state' => $replacement['state'], 'code' => 'replacement-code',
    ])->assertStatus(409);
    expect(DB::table('configuration_secrets')->where('resource_id', $id)->value('ciphertext'))->toBe($before)
        ->and(app(MailboxConnectionStore::class)->find('oauth-one', $id)->enabled)->toBeTrue();
});

it('rejects a Google grant for another mailbox without persisting its refresh token', function (): void {
    [$token] = oauthFixtures();
    Http::fake([
        'oauth2.googleapis.com/token' => Http::response([
            'access_token' => 'wrong-access', 'refresh_token' => 'wrong-refresh',
        ]),
        'gmail.googleapis.com/gmail/v1/users/me/profile' => Http::response(['emailAddress' => 'other@gmail.com']),
    ]);
    $base = 'https://localhost/email/mailbox-accounts/oauth-one/oauth';
    $start = $this->withToken($token)->postJson($base.'/start', [
        'provider' => 'google', 'mode' => 'imap', 'sending' => false, 'address' => 'user@gmail.com',
    ])->assertCreated()->json();
    $response = $this->withToken($token)->postJson($base.'/complete', [
        'provider' => 'google', 'state' => $start['state'], 'code' => 'wrong-mailbox-code',
        'callback_url' => 'https://zorvia.example.test/mail/oauth/callback',
    ])->assertStatus(409);
    expect($response->getContent())->not->toContain('wrong-refresh', 'other@gmail.com')
        ->and(DB::table('configuration_secrets')->count())->toBe(0);
});
