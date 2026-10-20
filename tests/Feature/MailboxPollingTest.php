<?php

declare(strict_types=1);

use Illuminate\Support\Facades\DB;
use Webong\Gateway\Accounts\AccountStore;
use Webong\Gateway\Email\EmailUpstreamCatalog;
use Webong\Gateway\Email\MailboxConnectionStore;
use Webong\Gateway\Email\MailboxPollStore;

it('configures account-owned IMAP polling on the shared scheduler without exposing its callback secret', function (): void {
    $catalog = app(EmailUpstreamCatalog::class);
    $catalog->create(['protocol' => 'imap', 'name' => 'poll-imap', 'address' => 'imap.test:993', 'server_name' => 'imap.test']);
    $catalog->create(['protocol' => 'smtp', 'name' => 'poll-smtp', 'address' => 'smtp.test:465', 'server_name' => 'smtp.test', 'tls_mode' => 'implicit']);
    $accounts = app(AccountStore::class);
    $token = $accounts->create('poll-owner');
    $otherToken = $accounts->create('other-owner');
    $input = ['address' => 'support@example.test', 'imap_upstream_id' => 'poll-imap',
        'smtp_upstream_id' => 'poll-smtp', 'imap_username' => 'support@example.test',
        'smtp_username' => 'support@example.test', 'imap_auth' => 'password',
        'smtp_auth' => 'password', 'imap_secret' => 'imap-password', 'smtp_secret' => 'smtp-password'];
    $connection = app(MailboxConnectionStore::class)->create('poll-owner', $input)['record'];
    app(MailboxConnectionStore::class)->markHealth('poll-owner', $connection->id, true);
    app(MailboxConnectionStore::class)->update('poll-owner', $connection->id, true);
    $endpoint = 'https://localhost/email/mailbox-accounts/poll-owner/connections/'.$connection->id.'/polling';
    $body = ['callback_url' => 'https://zorvia.example.test/webhooks/gateway-mail',
        'webhook_secret' => str_repeat('a', 64), 'enabled' => true];

    $this->withToken($otherToken)->putJson($endpoint, $body)->assertUnauthorized();
    $created = $this->withToken($token)->putJson($endpoint, $body)->assertCreated()->json();
    expect($created['route_id'])->toBeString()
        ->and($created['connection_id'])->toBe($connection->id)
        ->and($created['enabled'])->toBeTrue()
        ->and(json_encode($created))->not->toContain($body['webhook_secret']);
    $this->withToken($token)->getJson($endpoint)->assertOk()->assertJsonPath('route_id', $created['route_id'])
        ->assertDontSee($body['webhook_secret']);
    expect(DB::table('schedules')->where('origin', 'mailbox-poll:'.$created['route_id'])->where('language', 'native.imap')->exists())->toBeTrue();

    $this->withToken($token)->putJson($endpoint, $body)->assertOk()->assertJsonPath('route_id', $created['route_id']);
    $this->withToken($token)->putJson($endpoint, [...$body, 'callback_url' => 'http://zorvia.example.test/mail'])->assertUnprocessable();
    $this->withToken($token)->deleteJson($endpoint)->assertOk()->assertJsonPath('deleted', true);
    $this->withToken($token)->getJson($endpoint)->assertNotFound();
    expect(DB::table('schedules')->where('origin', 'mailbox-poll:'.$created['route_id'])->exists())->toBeFalse()
        ->and(DB::table('resource_checkpoints')->where('resource_id', $created['route_id'])->exists())->toBeFalse();
});

it('leases a native IMAP schedule and advances only acknowledged UIDs', function (): void {
    $catalog = app(EmailUpstreamCatalog::class);
    $catalog->create(['protocol' => 'imap', 'name' => 'poll-imap', 'address' => 'imap.test:993', 'server_name' => 'imap.test']);
    $catalog->create(['protocol' => 'smtp', 'name' => 'poll-smtp', 'address' => 'smtp.test:465', 'server_name' => 'smtp.test', 'tls_mode' => 'implicit']);
    app(AccountStore::class)->create('poll-owner');
    $connection = app(MailboxConnectionStore::class)->create('poll-owner', [
        'address' => 'support@example.test', 'imap_upstream_id' => 'poll-imap',
        'smtp_upstream_id' => 'poll-smtp', 'imap_username' => 'support@example.test',
        'smtp_username' => 'support@example.test', 'imap_auth' => 'password',
        'smtp_auth' => 'password', 'imap_secret' => 'imap-password', 'smtp_secret' => 'smtp-password'])['record'];
    app(MailboxConnectionStore::class)->markHealth('poll-owner', $connection->id, true);
    app(MailboxConnectionStore::class)->update('poll-owner', $connection->id, true);
    $poll = app(MailboxPollStore::class)->configure('poll-owner', $connection->id, [
        'callback_url' => 'https://zorvia.example.test/webhooks/gateway-mail',
        'webhook_secret' => str_repeat('a', 64), 'enabled' => true]);
    DB::table('schedules')->where('origin', 'mailbox-poll:'.$poll->id)->update(['next_run_at' => now('UTC')->subMinute()]);

    $run = $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/schedules/claim', ['automation_languages' => [], 'native_jobs' => ['imap']])
        ->assertOk()->json('data.0');
    expect($run['language'])->toBe('native.imap')->and($run['source'])->toBe($poll->id);
    $claim = $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/mailbox-polls/'.$poll->id.'/claim', [])->assertOk()->json();
    expect($claim['cursor'])->toBe(['uid_validity' => null, 'last_uid' => 0])
        ->and($claim['secret'])->toBe('imap-password');
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/mailbox-polls/'.$poll->id.'/claim', [])->assertConflict();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/mailbox-polls/'.$poll->id.'/ack', [
            'lease_token' => $claim['lease_token'], 'uid_validity' => 13, 'uid' => 7])->assertOk();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/mailbox-polls/'.$poll->id.'/release', [
            'lease_token' => $claim['lease_token']])->assertOk();
    $next = $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/mailbox-polls/'.$poll->id.'/claim', [])->assertOk()->json();
    expect($next['cursor'])->toBe(['uid_validity' => 13, 'last_uid' => 7]);
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/mailbox-polls/'.$poll->id.'/ack', [
            'lease_token' => $claim['lease_token'], 'uid_validity' => 13, 'uid' => 8])->assertConflict();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/mailbox-polls/'.$poll->id.'/ack', [
            'lease_token' => $next['lease_token'], 'uid_validity' => 14, 'uid' => 1])->assertOk();
});
