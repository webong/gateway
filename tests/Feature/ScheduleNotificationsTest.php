<?php

declare(strict_types=1);

use Illuminate\Http\Client\Request;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Http;

/** @return array{0: string, 1: string, 2: string} */
function claimNotifiedScheduleRun(mixed $case): array
{
    $notifications = app(\Webong\Gateway\Schedules\ScheduleNotificationConfig::class);
    if ($notifications->find() === null) {
        $notifications->create(['url' => 'https://ops.example.test/alerts', 'token' => 'operator-secret']);
    }
    $schedule = $case->withToken('registry-secret')->postJson('/schedules', [
        'name' => 'Notification check', 'cron' => '0 0 * * *',
        'language' => 'test-script', 'source' => 'run()',
        'payload' => ['private' => 'secret-payload'],
    ])->assertCreated()->json('id');
    $run = $case->withToken('registry-secret')->postJson('/schedules/'.$schedule.'/runs', [])
        ->assertAccepted()->json('id');
    $claim = $case->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/schedules/claim', ['automation_languages' => ['test-script']])
        ->assertOk()->json('data.0');
    expect($claim['id'])->toBe($run);

    return [$schedule, $run, $claim['claim_token']];
}

function completeNotifiedScheduleRun(mixed $case, string $run, string $token, string $status, int $queued): void
{
    $case->withHeader('X-Gateway-Internal', 'internal-secret')->postJson(
        '/_internal/gateway/schedule-runs/'.$run.'/complete',
        ['claim_token' => $token, 'status' => $status, 'deliveries_queued' => $queued,
            'error' => $status === 'failed' ? 'secret script error' : null],
    )->assertOk();
}

beforeEach(function (): void {
    config()->set('gateway.schedule_notifications.allowed_hosts', 'ops.example.test');
    config()->set('gateway.delivery_admin_url', 'http://spinner.internal:5052');
});

it('notifies once for a failed execution without leaking script or payload', function (): void {
    [$schedule, $run, $token] = claimNotifiedScheduleRun($this);
    completeNotifiedScheduleRun($this, $run, $token, 'failed', 0);
    expect(DB::table('schedule_notifications')->where('run_id', $run)->value('state'))->toBe('pending');
    Http::fake(['https://ops.example.test/alerts' => Http::response([], 204)]);

    $this->artisan('gateway:send-schedule-notifications')->assertSuccessful();
    $this->artisan('gateway:send-schedule-notifications')->assertSuccessful();

    Http::assertSentCount(1);
    Http::assertSent(fn (Request $request): bool => $request->url() === 'https://ops.example.test/alerts'
        && $request->hasHeader('Authorization', 'Bearer operator-secret')
        && $request->hasHeader('Idempotency-Key', 'gateway-schedule-run-'.$run)
        && $request['schedule_id'] === $schedule
        && $request['event'] === 'schedule.run.failed'
        && $request['outcome']['execution_status'] === 'failed'
        && ! str_contains($request->body(), 'secret script error')
        && ! str_contains($request->body(), 'secret-payload')
        && ! str_contains($request->body(), 'run()'));
    expect(DB::table('schedule_notifications')->where('run_id', $run)->value('state'))->toBe('sent');
    $this->withToken('registry-secret')->getJson('/schedules/'.$schedule.'/runs')->assertOk()
        ->assertJsonPath('data.0.notification_status', 'sent')
        ->assertJsonPath('data.0.notification_attempts', 0);
});

it('waits for durable delivery outcome and notifies on a terminal failure', function (): void {
    [$schedule, $run, $token] = claimNotifiedScheduleRun($this);
    completeNotifiedScheduleRun($this, $run, $token, 'succeeded', 1);
    Http::fake([
        'http://spinner.internal:5052/deliveries?*' => Http::sequence()
            ->push(['data' => [['id' => str_repeat('a', 32), 'event_id' => $run, 'state' => 'queued', 'attempts' => 0]]])
            ->push(['data' => [['id' => str_repeat('a', 32), 'event_id' => $run, 'state' => 'failed', 'attempts' => 1]]]),
        'https://ops.example.test/alerts' => Http::response([], 204),
    ]);

    $this->artisan('gateway:send-schedule-notifications')->assertSuccessful();
    expect(DB::table('schedule_notifications')->where('run_id', $run)->value('state'))->toBe('pending');
    Http::assertNotSent(fn (Request $request): bool => $request->url() === 'https://ops.example.test/alerts');

    DB::table('schedule_notifications')->where('run_id', $run)->update(['next_attempt_at' => now('UTC')->subSecond()]);
    $this->artisan('gateway:send-schedule-notifications')->assertSuccessful();
    Http::assertSent(fn (Request $request): bool => $request->url() === 'https://ops.example.test/alerts'
        && $request['schedule_id'] === $schedule
        && $request['outcome']['execution_status'] === 'succeeded'
        && $request['outcome']['delivery_status'] === 'failed'
        && ! str_contains($request->body(), 'secret-payload'));
    expect(DB::table('schedule_notifications')->where('run_id', $run)->value('state'))->toBe('sent');
});

it('retries failed alert handoff with the same idempotency key', function (): void {
    [, $run, $token] = claimNotifiedScheduleRun($this);
    completeNotifiedScheduleRun($this, $run, $token, 'failed', 0);
    Http::fake(['https://ops.example.test/alerts' => Http::sequence()->push([], 503)->push([], 204)]);

    $this->artisan('gateway:send-schedule-notifications')->assertSuccessful();
    $row = DB::table('schedule_notifications')->where('run_id', $run)->first();
    expect($row->state)->toBe('pending')->and($row->attempts)->toBe(1);
    DB::table('schedule_notifications')->where('run_id', $run)->update(['next_attempt_at' => now('UTC')->subSecond()]);

    $this->artisan('gateway:send-schedule-notifications')->assertSuccessful();
    Http::assertSentCount(2);
    Http::assertSent(fn (Request $request): bool => $request->hasHeader('Idempotency-Key', 'gateway-schedule-run-'.$run));
    expect(DB::table('schedule_notifications')->where('run_id', $run)->value('state'))->toBe('sent');
});

it('only lets an authenticated Spinner poll the notification outbox', function (): void {
    Http::fake();
    $this->postJson('/_internal/gateway/schedules/notify', [])->assertNotFound();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/schedules/notify', ['unsafe' => true])->assertUnprocessable();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/schedules/notify', [])->assertOk()
        ->assertJsonPath('checked', 0);
    Http::assertNothingSent();
});

it('resolves healthy runs without contacting the alert receiver', function (): void {
    [$schedule, $run, $token] = claimNotifiedScheduleRun($this);
    completeNotifiedScheduleRun($this, $run, $token, 'succeeded', 0);
    Http::fake();

    $this->artisan('gateway:send-schedule-notifications')->assertSuccessful();
    expect(DB::table('schedule_notifications')->where('run_id', $run)->value('state'))->toBe('resolved');
    $this->withToken('registry-secret')->getJson('/schedules/'.$schedule.'/runs')->assertOk()
        ->assertJsonPath('data.0.notification_status', 'resolved');
    Http::assertNothingSent();
});

it('recovers an expired notification claim and does not create alerts when disabled', function (): void {
    [, $run, $token] = claimNotifiedScheduleRun($this);
    completeNotifiedScheduleRun($this, $run, $token, 'failed', 0);
    DB::table('schedule_notifications')->where('run_id', $run)->update([
        'state' => 'checking', 'claim_token' => '00000000-0000-0000-0000-000000000000',
        'lease_until' => now('UTC')->subSecond(),
    ]);
    Http::fake(['https://ops.example.test/alerts' => Http::response([], 204)]);
    $this->artisan('gateway:send-schedule-notifications')->assertSuccessful();
    expect(DB::table('schedule_notifications')->where('run_id', $run)->value('state'))->toBe('sent');

    $notifications = app(\Webong\Gateway\Schedules\ScheduleNotificationConfig::class);
    $notifications->replace(['url' => 'https://ops.example.test/alerts', 'enabled' => false],
        $notifications->find()->revision);
    [, $disabledRun, $disabledToken] = claimNotifiedScheduleRun($this);
    completeNotifiedScheduleRun($this, $disabledRun, $disabledToken, 'failed', 0);
    expect(DB::table('schedule_notifications')->where('run_id', $disabledRun)->exists())->toBeFalse();
});

it('uses a CMS-managed destination without exposing its token and obeys the deployed host ceiling', function (): void {
    config()->set('gateway.schedule_notifications.allowed_hosts', 'ops.example.test');
    $this->postJson('/schedules/notification-destination', [
        'url' => 'https://ops.example.test/managed', 'token' => 'managed-secret',
    ])->assertUnauthorized();
    $this->withToken('registry-secret')->postJson('/schedules/notification-destination', [
        'url' => 'https://other.example.test/managed', 'token' => 'managed-secret',
    ])->assertUnprocessable();
    $created = $this->withToken('registry-secret')->postJson('/schedules/notification-destination', [
        'url' => 'https://ops.example.test/managed', 'token' => 'managed-secret',
    ])->assertCreated()->assertHeader('ETag', '"1"')->assertJsonPath('token_configured', true)
        ->assertDontSee('managed-secret')->json();
    expect(DB::table('configurations')->where('id', $created['id'])->value('document'))
        ->not->toContain('managed-secret');
    $this->withToken('registry-secret')->getJson('/schedules/notification-destination')
        ->assertOk()->assertDontSee('managed-secret');
    $this->withToken('registry-secret')
        ->getJson('/configurations/schedule.notification/'.$created['id'].'/revisions')
        ->assertOk()->assertDontSee('managed-secret');

    [, $run, $token] = claimNotifiedScheduleRun($this);
    completeNotifiedScheduleRun($this, $run, $token, 'failed', 0);
    Http::fake(['https://ops.example.test/managed' => Http::response([], 204)]);
    $this->artisan('gateway:send-schedule-notifications')->assertSuccessful();
    Http::assertSent(fn (Request $request): bool => $request->url() === 'https://ops.example.test/managed'
        && $request->hasHeader('Authorization', 'Bearer managed-secret'));

    $this->withToken('registry-secret')->withHeader('If-Match', '"1"')
        ->putJson('/schedules/notification-destination', [
            'url' => 'https://ops.example.test/managed', 'enabled' => false,
        ])->assertOk()->assertHeader('ETag', '"2"')->assertJsonPath('token_configured', true);
    $this->withToken('registry-secret')->withHeader('If-Match', '"1"')
        ->deleteJson('/schedules/notification-destination')->assertStatus(412);
    [, $disabledRun, $disabledToken] = claimNotifiedScheduleRun($this);
    completeNotifiedScheduleRun($this, $disabledRun, $disabledToken, 'failed', 0);
    expect(DB::table('schedule_notifications')->where('run_id', $disabledRun)->exists())->toBeFalse();

    $this->withToken('registry-secret')->withHeader('If-Match', '"2"')
        ->deleteJson('/schedules/notification-destination')->assertNoContent();
    expect(app(\Webong\Gateway\Schedules\ScheduleNotificationConfig::class)->effective())->toBeNull();
});

it('stops CMS-managed alerts when the deployed receiver hostname is withdrawn', function (): void {
    config()->set('gateway.schedule_notifications.allowed_hosts', 'ops.example.test');
    $this->withToken('registry-secret')->postJson('/schedules/notification-destination', [
        'url' => 'https://ops.example.test/managed',
    ])->assertCreated();
    [, $run, $token] = claimNotifiedScheduleRun($this);
    completeNotifiedScheduleRun($this, $run, $token, 'failed', 0);
    config()->set('gateway.schedule_notifications.allowed_hosts', '');
    Http::fake();
    $this->artisan('gateway:send-schedule-notifications')->assertSuccessful();
    Http::assertNothingSent();
    expect(DB::table('schedule_notifications')->where('run_id', $run)->value('state'))->toBe('pending');
});
