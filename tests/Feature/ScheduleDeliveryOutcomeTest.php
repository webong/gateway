<?php

declare(strict_types=1);

use Illuminate\Http\Client\Request;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Http;

/** @return array{0: string, 1: string} */
function createScheduleOutcomeRun(mixed $case): array
{
    $schedule = $case->withToken('registry-secret')->postJson('/schedules', [
        'name' => 'Outcome check', 'cron' => '0 0 * * *',
        'language' => 'test-script', 'source' => 'run()',
    ])->assertCreated()->json('id');
    $run = $case->withToken('registry-secret')->postJson('/schedules/'.$schedule.'/runs', [])
        ->assertAccepted()->json('id');

    return [$schedule, $run];
}

beforeEach(function (): void {
    config()->set('gateway.delivery_admin_url', 'http://spinner.internal:5052');
});

it('authenticates run outcomes and handles pending or no-delivery runs without contacting Spinner', function (): void {
    [$schedule, $run] = createScheduleOutcomeRun($this);
    Http::fake();
    $url = '/schedules/'.$schedule.'/runs/'.$run.'/outcome';

    $this->withToken('wrong-token')->getJson($url)->assertUnauthorized();
    $this->withToken('registry-secret')->getJson('/schedules/'.$schedule.'/runs/missing/outcome')->assertNotFound();
    $this->withToken('registry-secret')->getJson($url)->assertOk()
        ->assertJsonPath('execution_status', 'pending')
        ->assertJsonPath('delivery_status', 'pending');

    DB::table('schedule_runs')->where('id', $run)->update(['status' => 'succeeded']);
    $this->withToken('registry-secret')->getJson($url)->assertOk()
        ->assertJsonPath('delivery_status', 'none')
        ->assertJsonPath('deliveries_observed', 0);
    Http::assertNothingSent();
});

it('aggregates delivered and failed receipts without confusing them with script execution', function (): void {
    [$schedule, $run] = createScheduleOutcomeRun($this);
    DB::table('schedule_runs')->where('id', $run)->update(['status' => 'succeeded', 'deliveries_queued' => 2]);
    Http::fake(['http://spinner.internal:5052/deliveries?*' => Http::response(['data' => [
        ['id' => str_repeat('a', 32), 'event_id' => $run, 'state' => 'delivered', 'attempts' => 1],
        ['id' => str_repeat('b', 32), 'event_id' => $run, 'state' => 'failed', 'attempts' => 8,
            'target' => 'https://secret.example.test', 'payload' => 'private'],
    ]])]);

    $this->withToken('registry-secret')->getJson('/schedules/'.$schedule.'/runs/'.$run.'/outcome')->assertOk()
        ->assertJsonPath('execution_status', 'succeeded')
        ->assertJsonPath('delivery_status', 'failed')
        ->assertJsonPath('deliveries_queued', 2)
        ->assertJsonPath('deliveries_observed', 2)
        ->assertJsonPath('counts.failed', 1)
        ->assertJsonPath('counts.delivered', 1)
        ->assertJsonMissingPath('target')
        ->assertJsonMissingPath('payload');
    Http::assertSent(fn (Request $request): bool => $request->url() === 'http://spinner.internal:5052/deliveries?event_id='.$run.'&limit=100'
        && $request->hasHeader('Authorization', 'Bearer internal-secret'));
});

it('reports delivery only when every expected receipt is delivered across nodes', function (): void {
    [$schedule, $run] = createScheduleOutcomeRun($this);
    DB::table('schedule_runs')->where('id', $run)->update(['status' => 'succeeded', 'deliveries_queued' => 2]);
    config()->set('gateway.delivery_admin_nodes', [
        'north' => 'http://north.internal:5052', 'south' => 'http://south.internal:5052',
    ]);
    Http::fake([
        'http://north.internal:5052/deliveries?*' => Http::response(['data' => [[
            'id' => str_repeat('e', 32), 'event_id' => $run, 'state' => 'delivered', 'attempts' => 1,
        ]]]),
        'http://south.internal:5052/deliveries?*' => Http::response(['data' => [[
            'id' => str_repeat('f', 32), 'event_id' => $run, 'state' => 'delivered', 'attempts' => 2,
        ]]]),
    ]);

    $this->withToken('registry-secret')->getJson('/schedules/'.$schedule.'/runs/'.$run.'/outcome')->assertOk()
        ->assertJsonPath('delivery_status', 'delivered')
        ->assertJsonPath('deliveries_observed', 2)
        ->assertJsonPath('counts.delivered', 2);
    Http::assertSentCount(2);
});

it('reports incomplete receipts as unknown across nodes', function (): void {
    [$schedule, $run] = createScheduleOutcomeRun($this);
    DB::table('schedule_runs')->where('id', $run)->update(['status' => 'succeeded', 'deliveries_queued' => 2]);
    config()->set('gateway.delivery_admin_nodes', [
        'north' => 'http://north.internal:5052', 'south' => 'http://south.internal:5052',
    ]);
    Http::fake([
        'http://north.internal:5052/deliveries?*' => Http::response(['data' => [[
            'id' => str_repeat('c', 32), 'event_id' => $run, 'state' => 'delivered', 'attempts' => 1,
        ]]]),
        'http://south.internal:5052/deliveries?*' => Http::response(['data' => []]),
    ]);
    $url = '/schedules/'.$schedule.'/runs/'.$run.'/outcome';
    $this->withToken('registry-secret')->getJson($url)->assertOk()
        ->assertJsonPath('delivery_status', 'unknown')
        ->assertJsonPath('deliveries_observed', 1);
});

it('does not mistake duplicate receipts for a complete delivery set', function (): void {
    [$schedule, $run] = createScheduleOutcomeRun($this);
    DB::table('schedule_runs')->where('id', $run)->update(['status' => 'succeeded', 'deliveries_queued' => 1]);
    Http::fake(['http://spinner.internal:5052/deliveries?*' => Http::response(['data' => [
        ['id' => str_repeat('a', 32), 'event_id' => $run, 'state' => 'delivered', 'attempts' => 1],
        ['id' => str_repeat('b', 32), 'event_id' => $run, 'state' => 'delivered', 'attempts' => 1],
    ]])]);

    $this->withToken('registry-secret')->getJson('/schedules/'.$schedule.'/runs/'.$run.'/outcome')->assertOk()
        ->assertJsonPath('delivery_status', 'unknown')
        ->assertJsonPath('deliveries_observed', 2);
});

it('fails closed when a delivery node is unavailable', function (): void {
    [$schedule, $run] = createScheduleOutcomeRun($this);
    DB::table('schedule_runs')->where('id', $run)->update(['status' => 'succeeded', 'deliveries_queued' => 2]);
    config()->set('gateway.delivery_admin_nodes', [
        'north' => 'http://north.internal:5052', 'south' => 'http://south.internal:5052',
    ]);
    Http::fake([
        'http://north.internal:5052/deliveries?*' => Http::response(['data' => []]),
        'http://south.internal:5052/deliveries?*' => Http::response(['message' => 'Unavailable'], 503),
    ]);
    $this->withToken('registry-secret')->getJson('/schedules/'.$schedule.'/runs/'.$run.'/outcome')->assertServiceUnavailable();
});

it('fails closed on an unsafe Spinner status response', function (): void {
    [$schedule, $run] = createScheduleOutcomeRun($this);
    DB::table('schedule_runs')->where('id', $run)->update(['status' => 'succeeded', 'deliveries_queued' => 2]);
    config()->set('gateway.delivery_admin_nodes', [
        'north' => 'http://north.internal:5052', 'south' => 'http://south.internal:5052',
    ]);
    Http::fake([
        'http://north.internal:5052/deliveries?*' => Http::response(['data' => [[
            'id' => str_repeat('d', 32), 'event_id' => 'wrong-run', 'state' => 'delivered', 'attempts' => 1,
        ]]]),
        'http://south.internal:5052/deliveries?*' => Http::response(['data' => []]),
    ]);
    $this->withToken('registry-secret')->getJson('/schedules/'.$schedule.'/runs/'.$run.'/outcome')->assertServiceUnavailable();
});
