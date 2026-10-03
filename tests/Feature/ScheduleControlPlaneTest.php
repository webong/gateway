<?php

declare(strict_types=1);

use Illuminate\Support\Facades\DB;

afterEach(function (): void {
    DB::table('schedule_runs')->where('kind', 'manual')->delete();
});

it('creates and updates authenticated schedules with run history', function (): void {
    $this->postJson('/schedules', [])->assertUnauthorized();

    $schedule = $this->withToken('registry-secret')->postJson('/schedules', [
        'name' => 'Check upstream',
        'cron' => '* * * * *',
        'language' => 'test-script',
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
    $base = ['name' => 'bad', 'cron' => 'not cron', 'language' => 'test-script', 'source' => 'gateway.respond("ok")'];
    $this->withToken('registry-secret')->postJson('/schedules', $base)->assertUnprocessable();
    $this->withToken('registry-secret')->postJson('/schedules', array_replace($base, [
        'cron' => '* * * * *', 'source' => '   ',
    ]))->assertUnprocessable();
    $this->withToken('registry-secret')->postJson('/schedules', array_replace($base, [
        'cron' => '* * * * *', 'language' => 'disabled-script',
    ]))->assertUnprocessable();
});

it('claims once, rejects stale tokens and records completion', function (): void {
    $id = $this->withToken('registry-secret')->postJson('/schedules', [
        'name' => 'Minute job', 'cron' => '* * * * *',
        'language' => 'test-script', 'source' => 'gateway.respond("ok")',
    ])->assertCreated()->json('id');
    DB::table('schedules')->where('id', $id)->update(['next_run_at' => now('UTC')->subMinute()]);

    $claim = $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/schedules/claim', [
            'automation_languages' => ['test-script'],
        ])->assertOk()->json('data.0');
    expect($claim['schedule_id'])->toBe($id)
        ->and($claim['language'])->toBe('test-script');
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/schedules/claim', [
            'automation_languages' => ['test-script'],
        ])->assertOk()->assertJsonCount(0, 'data');

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
        'language' => 'test-script', 'source' => 'gateway.respond("ok")',
    ])->assertCreated()->json('id');
    DB::table('schedules')->where('id', $id)->update(['next_run_at' => now('UTC')->subMinute()]);
    $first = $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/schedules/claim', [
            'automation_languages' => ['test-script'],
        ])->assertOk()->json('data.0');
    DB::table('schedule_runs')->where('id', $first['id'])->update(['lease_until' => now('UTC')->subSecond()]);
    $second = $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/schedules/claim', [
            'automation_languages' => ['test-script'],
        ])->assertOk()->json('data.0');
    expect($second['id'])->toBe($first['id'])
        ->and($second['claim_token'])->not->toBe($first['claim_token'])
        ->and(DB::table('schedule_runs')->count())->toBe(1);

    $this->withToken('registry-secret')->patchJson('/schedules/'.$id, ['enabled' => false])->assertOk();
    DB::table('schedule_runs')->where('id', $first['id'])->update(['lease_until' => now('UTC')->subSecond()]);
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/schedules/claim', [
            'automation_languages' => ['test-script'],
        ])->assertOk()->assertJsonCount(0, 'data');
});

it('claims only schedules supported by the active Spinner worker', function (): void {
    $id = $this->withToken('registry-secret')->postJson('/schedules', [
        'name' => 'Capability-aware job',
        'cron' => '* * * * *',
        'language' => 'test-script',
        'source' => 'run()',
    ])->assertCreated()->json('id');
    DB::table('schedules')->where('id', $id)->update(['next_run_at' => now('UTC')->subMinute()]);

    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/schedules/claim', [])
        ->assertOk()->assertJsonCount(0, 'data');

    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/schedules/claim', [
            'automation_languages' => [],
        ])->assertOk()->assertJsonCount(0, 'data');

    expect(DB::table('schedule_runs')->count())->toBe(0)
        ->and(DB::table('schedules')->where('id', $id)->value('next_run_at'))
        ->not->toBeNull();
});

it('queues idempotent manual runs through the capability-aware claim path', function (): void {
    $schedule = $this->withToken('registry-secret')->postJson('/schedules', [
        'name' => 'Manual check', 'cron' => '0 0 * * *',
        'language' => 'test-script', 'source' => 'original()',
    ])->assertCreated()->json();
    $id = $schedule['id'];
    $this->withToken('registry-secret')->patchJson('/schedules/'.$id, ['enabled' => false])->assertOk();

    $first = $this->withToken('registry-secret')->withHeader('Idempotency-Key', 'operator-1')
        ->postJson('/schedules/'.$id.'/runs', [])->assertAccepted()
        ->assertJsonPath('kind', 'manual')->assertJsonPath('status', 'pending')->json();
    expect($first['deliveries_url'])->toBe('/deliveries?event_id='.$first['id']);
    expect($first['outcome_url'])->toBe('/schedules/'.$id.'/runs/'.$first['id'].'/outcome');
    $same = $this->withToken('registry-secret')->withHeader('Idempotency-Key', 'operator-1')
        ->postJson('/schedules/'.$id.'/runs', [])->assertAccepted()->json();
    $second = $this->withToken('registry-secret')->withHeader('Idempotency-Key', 'operator-2')
        ->postJson('/schedules/'.$id.'/runs', [])->assertAccepted()->json();

    expect($same['id'])->toBe($first['id'])
        ->and($second['id'])->not->toBe($first['id'])
        ->and(DB::table('schedule_runs')->where('schedule_id', $id)->count())->toBe(2)
        ->and(DB::table('schedules')->where('id', $id)->value('next_run_at'))->toBe($schedule['next_run_at']);

    $this->withToken('registry-secret')->patchJson('/schedules/'.$id, ['source' => 'edited()'])->assertOk();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/schedules/claim', ['automation_languages' => ['disabled-script']])
        ->assertOk()->assertJsonCount(0, 'data');
    $claimResponse = $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/schedules/claim', ['automation_languages' => ['test-script']])
        ->assertOk()->assertJsonCount(2, 'data');
    expect(json_decode($claimResponse->getContent(), false, 512, JSON_THROW_ON_ERROR)->data[0]->payload)
        ->toBeInstanceOf(stdClass::class);
    $claims = $claimResponse->json('data');
    expect($claims[0]['source'])->toBe('original()')
        ->and($claims[1]['source'])->toBe('original()');

    $this->withHeader('X-Gateway-Internal', 'internal-secret')->postJson(
        '/_internal/gateway/schedule-runs/'.$claims[0]['id'].'/complete',
        ['claim_token' => $claims[0]['claim_token'], 'status' => 'succeeded', 'deliveries_queued' => 0],
    )->assertOk();
    $this->withToken('registry-secret')->getJson('/schedules/'.$id.'/runs')->assertOk()
        ->assertJsonCount(2, 'data');
});

it('rejects unauthenticated and malformed manual run requests', function (): void {
    $id = $this->withToken('registry-secret')->postJson('/schedules', [
        'name' => 'Manual check', 'cron' => '0 0 * * *',
        'language' => 'test-script', 'source' => 'run()',
    ])->assertCreated()->json('id');

    $this->withToken('wrong-token')->postJson('/schedules/'.$id.'/runs', [])->assertUnauthorized();
    $this->withToken('registry-secret')->postJson('/schedules/missing/runs', [])->assertNotFound();
    $this->withToken('registry-secret')->postJson('/schedules/'.$id.'/runs', ['source' => 'override()'])
        ->assertUnprocessable();
    $this->withToken('registry-secret')->withHeader('Idempotency-Key', 'unsafe key')
        ->postJson('/schedules/'.$id.'/runs', [])->assertUnprocessable();
    expect(DB::table('schedule_runs')->count())->toBe(0);
});

it('backfills cron occurrence identities when upgrading an existing run table', function (): void {
    $id = $this->withToken('registry-secret')->postJson('/schedules', [
        'name' => 'Existing cron', 'cron' => '* * * * *',
        'language' => 'test-script', 'source' => 'run()',
    ])->assertCreated()->json('id');
    DB::table('schedules')->where('id', $id)->update(['next_run_at' => now('UTC')->subMinute()]);
    $claim = $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/schedules/claim', ['automation_languages' => ['test-script']])
        ->assertOk()->json('data.0');

    $migration = require dirname(__DIR__, 2).'/src/planner/database/migrations/2026_09_28_000009_add_manual_schedule_runs.php';
    $migration->down();
    $migration->up();

    expect(DB::table('schedule_runs')->where('id', $claim['id'])->value('kind'))->toBe('cron')
        ->and(DB::table('schedule_runs')->where('id', $claim['id'])->value('occurrence_key'))
        ->toBe('cron:'.$claim['scheduled_for']);
});

it('idempotently registers schedules emitted by scripts', function (): void {
    $payload = [
        'origin' => 'endpoint:endpoint-1',
        'schedules' => [[
            'key' => 'daily-report',
            'name' => 'Daily report',
            'cron' => '0 9 * * *',
            'timezone' => 'Africa/Lagos',
            'language' => 'test-script',
            'source' => 'gateway.respond("done", 200)',
            'payload' => ['team' => 'ops'],
        ]],
    ];

    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/schedules/register', $payload)
        ->assertOk()->assertJsonPath('registered', 1);
    $row = DB::table('schedules')->sole();
    expect($row->origin)->toBe('endpoint:endpoint-1')
        ->and($row->key)->toBe('daily-report')
        ->and($row->next_run_at)->not->toBeNull();

    $payload['schedules'][0]['name'] = 'Updated daily report';
    $payload['schedules'][0]['enabled'] = false;
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/schedules/register', $payload)
        ->assertOk();

    expect(DB::table('schedules')->count())->toBe(1)
        ->and(DB::table('schedules')->value('name'))->toBe('Updated daily report')
        ->and(DB::table('schedules')->value('enabled'))->toBe(0)
        ->and(DB::table('schedules')->value('next_run_at'))->toBeNull();

    $this->withToken('registry-secret')->getJson('/schedules/'.$row->id)->assertOk()
        ->assertJsonPath('origin', 'endpoint:endpoint-1')
        ->assertJsonPath('key', 'daily-report');
});

it('rejects malformed or duplicate script schedule commands', function (): void {
    $command = [
        'key' => 'duplicate', 'name' => 'Duplicate', 'cron' => '* * * * *',
        'language' => 'test-script', 'source' => 'run()',
    ];

    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/schedules/register', [
            'origin' => 'endpoint:endpoint-1',
            'schedules' => [$command, $command],
        ])->assertUnprocessable();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/schedules/register', [
            'origin' => 'endpoint:endpoint-1',
            'schedules' => [array_replace($command, ['unknown' => true])],
        ])->assertUnprocessable();
});
