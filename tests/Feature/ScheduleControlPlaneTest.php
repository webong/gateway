<?php

declare(strict_types=1);

use Illuminate\Support\Facades\DB;

it('creates and updates authenticated schedules with run history', function (): void {
    $this->postJson('/schedules', [])->assertUnauthorized();

    $schedule = $this->withToken('registry-secret')->postJson('/schedules', [
        'name' => 'Check upstream',
        'cron' => '* * * * *',
        'language' => 'javascript',
        'source' => "gateway.forward({url: 'https://example.test/check'})",
        'payload' => ['account' => 42],
    ])->assertCreated()->json();

    expect($schedule['timezone'])->toBe('UTC')
        ->and($schedule['next_run_at'])->not->toBeNull();

    $this->withToken('registry-secret')->getJson('/schedules/'.$schedule['id'])->assertOk()
        ->assertJsonPath('payload.account', 42);
    $this->withToken('registry-secret')->getJson('/schedules/'.$schedule['id'].'/runs')->assertOk()
        ->assertJsonCount(0, 'data');
    $this->withToken('registry-secret')->patchJson('/schedules/'.$schedule['id'], ['enabled' => false])
        ->assertOk()->assertJsonPath('enabled', false);
    $this->withToken('registry-secret')->deleteJson('/schedules/'.$schedule['id'])->assertNoContent();
});

it('rejects invalid cron and source', function (): void {
    $base = ['name' => 'bad', 'cron' => 'not cron', 'language' => 'lua', 'source' => 'gateway.respond("ok")'];
    $this->withToken('registry-secret')->postJson('/schedules', $base)->assertUnprocessable();
    $this->withToken('registry-secret')->postJson('/schedules', array_replace($base, [
        'cron' => '* * * * *', 'source' => '   ',
    ]))->assertUnprocessable();
});

it('claims once, rejects stale tokens and records completion', function (): void {
    $id = $this->withToken('registry-secret')->postJson('/schedules', [
        'name' => 'Minute job', 'cron' => '* * * * *',
        'language' => 'lua', 'source' => 'gateway.respond("ok")',
    ])->assertCreated()->json('id');
    DB::table('schedules')->where('id', $id)->update(['next_run_at' => now('UTC')->subMinute()]);

    $claim = $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/schedules/claim', [])->assertOk()->json('data.0');
    expect($claim['schedule_id'])->toBe($id)
        ->and($claim['language'])->toBe('lua');
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/schedules/claim', [])->assertOk()->assertJsonCount(0, 'data');

    $this->withHeader('X-Gateway-Internal', 'internal-secret')->postJson(
        '/_internal/gateway/schedule-runs/'.$claim['id'].'/complete',
        ['claim_token' => '00000000-0000-0000-0000-000000000000', 'status' => 'succeeded', 'deliveries_queued' => 0],
    )->assertStatus(409);
    $this->withHeader('X-Gateway-Internal', 'internal-secret')->postJson(
        '/_internal/gateway/schedule-runs/'.$claim['id'].'/complete',
        ['claim_token' => $claim['claim_token'], 'status' => 'succeeded', 'deliveries_queued' => 1],
    )->assertOk();
    $this->withToken('registry-secret')->getJson('/schedules/'.$id.'/runs')->assertOk()
        ->assertJsonPath('data.0.status', 'succeeded')
        ->assertJsonPath('data.0.deliveries_queued', 1);
});

it('reclaims expired leases without creating a second occurrence', function (): void {
    $id = $this->withToken('registry-secret')->postJson('/schedules', [
        'name' => 'Minute job', 'cron' => '* * * * *',
        'language' => 'lua', 'source' => 'gateway.respond("ok")',
    ])->assertCreated()->json('id');
    DB::table('schedules')->where('id', $id)->update(['next_run_at' => now('UTC')->subMinute()]);
    $first = $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/schedules/claim', [])->assertOk()->json('data.0');
    DB::table('schedule_runs')->where('id', $first['id'])->update(['lease_until' => now('UTC')->subSecond()]);
    $second = $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/schedules/claim', [])->assertOk()->json('data.0');
    expect($second['id'])->toBe($first['id'])
        ->and($second['claim_token'])->not->toBe($first['claim_token'])
        ->and(DB::table('schedule_runs')->count())->toBe(1);

    $this->withToken('registry-secret')->patchJson('/schedules/'.$id, ['enabled' => false])->assertOk();
    DB::table('schedule_runs')->where('id', $first['id'])->update(['lease_until' => now('UTC')->subSecond()]);
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/schedules/claim', [])->assertOk()->assertJsonCount(0, 'data');
});
