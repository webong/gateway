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
        ->and($created['initial_sync'])->toBe('new')
        ->and($created['status']['state'])->toBe('awaiting_first_run')
        ->and($created['status']['last_run_at'])->toBeNull()
        ->and($created['status']['last_success_at'])->toBeNull()
        ->and($created['status']['last_failure_at'])->toBeNull()
        ->and($created['status']['next_run_at'])->toBeString()
        ->and(json_encode($created))->not->toContain($body['webhook_secret']);
    $this->withToken($otherToken)->getJson($endpoint)->assertUnauthorized();
    $this->withToken($token)->getJson($endpoint)->assertOk()->assertJsonPath('route_id', $created['route_id'])
        ->assertDontSee($body['webhook_secret']);
    expect(DB::table('schedules')->where('origin', 'mailbox-poll:'.$created['route_id'])->where('language', 'native.imap')->exists())->toBeTrue();

    $this->withToken($token)->putJson($endpoint, [...$body, 'initial_sync' => 'all'])
        ->assertOk()->assertJsonPath('initial_sync', 'all');
    $this->withToken($token)->putJson($endpoint, $body)->assertOk()->assertJsonPath('initial_sync', 'all');

    $this->withToken($token)->putJson($endpoint, $body)->assertOk()->assertJsonPath('route_id', $created['route_id']);
    $this->withToken($token)->putJson($endpoint, [...$body, 'callback_url' => 'http://zorvia.example.test/mail'])->assertUnprocessable();
    $this->withToken($token)->deleteJson($endpoint)->assertOk()->assertJsonPath('deleted', true);
    $this->withToken($token)->getJson($endpoint)->assertNotFound();
    expect(DB::table('schedules')->where('origin', 'mailbox-poll:'.$created['route_id'])->exists())->toBeFalse()
        ->and(DB::table('resource_checkpoints')->where('resource_id', $created['route_id'])->exists())->toBeFalse();
});

it('reports account-owned poll health without exposing worker failure details', function (): void {
    $catalog = app(EmailUpstreamCatalog::class);
    $catalog->create(['protocol' => 'imap', 'name' => 'poll-imap', 'address' => 'imap.test:993', 'server_name' => 'imap.test']);
    $token = app(AccountStore::class)->create('poll-owner');
    $connection = app(MailboxConnectionStore::class)->createImap('poll-owner', [
        'address' => 'support@example.test', 'imap_upstream_id' => 'poll-imap',
        'imap_username' => 'support@example.test', 'imap_auth' => 'password',
        'imap_secret' => 'imap-password'])['record'];
    app(MailboxConnectionStore::class)->markHealth('poll-owner', $connection->id, true);
    app(MailboxConnectionStore::class)->update('poll-owner', $connection->id, true);
    $endpoint = 'https://localhost/email/mailbox-accounts/poll-owner/imap-connections/'.$connection->id.'/polling';
    $route = $this->withToken($token)->putJson($endpoint, [
        'callback_url' => 'https://zorvia.example.test/webhooks/mailflow',
        'webhook_secret' => str_repeat('a', 64), 'enabled' => true,
    ])->assertCreated()->json('route_id');
    $schedule = DB::table('schedules')->where('origin', 'mailbox-poll:'.$route)->first();

    DB::table('schedules')->where('id', $schedule->id)->update(['next_run_at' => now('UTC')->subMinute()]);
    $failed = app(\Webong\Gateway\Schedules\ScheduleStore::class)->claim(languages: ['native.imap'])[0];
    app(\Webong\Gateway\Schedules\ScheduleStore::class)->complete(
        $failed['id'], $failed['claim_token'], 'failed', 0, 'IMAP password and private host details',
    );
    $failure = $this->withToken($token)->getJson($endpoint)->assertOk()->json('status');
    expect($failure['state'])->toBe('failed')
        ->and($failure['last_run_at'])->toBeString()
        ->and($failure['last_failure_at'])->toBeString()
        ->and($failure['last_success_at'])->toBeNull()
        ->and(json_encode($failure))->not->toContain('password', 'private host');

    DB::table('schedules')->where('id', $schedule->id)->update(['next_run_at' => now('UTC')->subSeconds(30)]);
    $succeeded = app(\Webong\Gateway\Schedules\ScheduleStore::class)->claim(languages: ['native.imap'])[0];
    app(\Webong\Gateway\Schedules\ScheduleStore::class)->complete(
        $succeeded['id'], $succeeded['claim_token'], 'succeeded', 0, null,
    );
    $success = $this->withToken($token)->getJson($endpoint)->assertOk()->json('status');
    expect($success['state'])->toBe('healthy')
        ->and($success['last_success_at'])->toBeString()
        ->and($success['last_failure_at'])->toBeString();
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
    expect($claim['cursor'])->toBe(['uid_validity' => null, 'last_uid' => 0, 'baseline_pending' => false])
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
    expect($next['cursor'])->toBe(['uid_validity' => 13, 'last_uid' => 7, 'baseline_pending' => false]);
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/mailbox-polls/'.$poll->id.'/ack', [
            'lease_token' => $claim['lease_token'], 'uid_validity' => 13, 'uid' => 8])->assertConflict();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/mailbox-polls/'.$poll->id.'/ack', [
            'lease_token' => $next['lease_token'], 'uid_validity' => 14, 'uid' => 1])->assertOk();
});

it('resets a paused poll to the current mailbox position before it can be enabled', function (): void {
    $catalog = app(EmailUpstreamCatalog::class);
    $catalog->create(['protocol' => 'imap', 'name' => 'poll-imap', 'address' => 'imap.test:993', 'server_name' => 'imap.test']);
    $token = app(AccountStore::class)->create('longomba');
    $connection = app(MailboxConnectionStore::class)->createImap('longomba', [
        'address' => 'support@longomba.example', 'imap_upstream_id' => 'poll-imap',
        'imap_username' => 'support@longomba.example', 'imap_auth' => 'password', 'imap_secret' => 'imap-password',
    ])['record'];
    app(MailboxConnectionStore::class)->markHealth('longomba', $connection->id, true);
    app(MailboxConnectionStore::class)->update('longomba', $connection->id, true);
    $endpoint = 'https://localhost/email/mailbox-accounts/longomba/connections/'.$connection->id.'/polling';
    $body = ['callback_url' => 'https://zorvia.example.test/webhooks/mailflow',
        'webhook_secret' => str_repeat('b', 64), 'enabled' => false];

    $created = $this->withToken($token)->putJson($endpoint, $body)->assertCreated()->json();
    expect($created['initial_sync'])->toBe('new')
        ->and($created['cursor'])->toBe(['uid_validity' => null, 'last_uid' => 0, 'baseline_pending' => false]);

    DB::table('resource_checkpoints')->where('resource_id', $created['route_id'])->update([
        'cursor' => json_encode(['uid_validity' => null, 'last_uid' => 0], JSON_THROW_ON_ERROR),
    ]);
    $this->withToken($token)->putJson($endpoint, [...$body, 'enabled' => true])->assertConflict();

    $reset = $this->withToken($token)->withHeader('Idempotency-Key', 'longomba-reset-1')
        ->postJson($endpoint.'/reset-to-current')->assertAccepted()->json();
    expect($reset['route_id'])->toBe($created['route_id'])
        ->and($reset['baseline_pending'])->toBeTrue()
        ->and($reset['enabled'])->toBeFalse();
    $schedule = DB::table('schedules')->where('origin', 'mailbox-poll:'.$created['route_id'])->first();
    expect((bool) $schedule->enabled)->toBeFalse()
        ->and(json_decode($schedule->payload, true))->toBe(['action' => 'baseline']);

    $retry = $this->withToken($token)->withHeader('Idempotency-Key', 'longomba-reset-1')
        ->postJson($endpoint.'/reset-to-current')->assertAccepted()->json();
    expect($retry['run_id'])->toBe($reset['run_id']);

    $run = app(\Webong\Gateway\Schedules\ScheduleStore::class)->claim(languages: ['native.imap'])[0];
    expect((array) $run['payload'])->toBe(['action' => 'baseline']);
    $claim = $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/mailbox-polls/'.$created['route_id'].'/claim', ['action' => 'baseline'])
        ->assertOk()->json();
    expect($claim['cursor']['baseline_pending'])->toBeTrue();

    $this->withToken($token)->putJson($endpoint, [...$body, 'enabled' => true])->assertConflict();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/mailbox-polls/'.$created['route_id'].'/baseline', [
            'lease_token' => $claim['lease_token'], 'uid_validity' => 13, 'uid' => 5,
        ])->assertOk()->assertJsonPath('baselined', true);
    $afterBaseline = DB::table('resource_checkpoints')->where('resource_id', $created['route_id'])->first();
    expect(json_decode($afterBaseline->cursor, true))
        ->toBe(['uid_validity' => 13, 'last_uid' => 5, 'baseline_pending' => false]);

    $this->withToken($token)->putJson($endpoint, [...$body, 'enabled' => true])->assertOk();
    $this->withToken($token)->withHeader('Idempotency-Key', 'longomba-reset-1')
        ->postJson($endpoint.'/reset-to-current')->assertAccepted();
    expect(json_decode(DB::table('resource_checkpoints')->where('resource_id', $created['route_id'])->value('cursor'), true))
        ->toBe(['uid_validity' => 13, 'last_uid' => 5, 'baseline_pending' => false]);
    $this->withToken($token)->withHeader('Idempotency-Key', 'longomba-reset-2')
        ->postJson($endpoint.'/reset-to-current')->assertConflict();
});
