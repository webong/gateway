<?php

declare(strict_types=1);

use Illuminate\Http\Client\Request;
use Illuminate\Support\Facades\Http;

const SHELL_RUN_ID = '0123456789abcdef0123456789abcdef';

beforeEach(function (): void {
    config()->set('gateway.delivery_admin_url', 'http://spinner.internal:5052');
    config()->set('gateway.delivery_admin_nodes', null);
    config()->set('gateway.delivery_admin_shared_urls', null);
    config()->set('gateway.delivery_admin_discovery_enabled', false);
});

it('requires registry authentication before looking up a shell run', function (): void {
    Http::fake();

    $this->getJson('/shell/runs/'.SHELL_RUN_ID)->assertUnauthorized();
    Http::assertNothingSent();
});

it('returns only validated private run history from Spinner', function (): void {
    Http::fake(['http://spinner.internal:5052/shell/runs/'.SHELL_RUN_ID => Http::response([
        'id' => SHELL_RUN_ID,
        'hook' => 'github/deploy',
        'delivery_id' => 'delivery-123',
        'started_at' => '2026-10-03T12:00:00Z',
        'duration_ms' => 42,
        'exit_code' => 0,
        'failed' => false,
        'private_unknown_field' => 'not-for-clients',
    ])]);

    $this->withToken('registry-secret')->getJson('/shell/runs/'.SHELL_RUN_ID)->assertOk()
        ->assertJsonPath('data.id', SHELL_RUN_ID)
        ->assertJsonPath('data.hook', 'github/deploy')
        ->assertJsonPath('data.node', 'local')
        ->assertJsonMissingPath('data.private_unknown_field')
        ->assertHeader('Cache-Control', 'no-store, private');

    Http::assertSent(fn (Request $request): bool => $request->url() === 'http://spinner.internal:5052/shell/runs/'.SHELL_RUN_ID
        && $request->hasHeader('Authorization', 'Bearer internal-secret'));
});

it('checks every node before declaring a shell run missing', function (): void {
    config()->set('gateway.delivery_admin_nodes', [
        'north' => 'http://north.internal:5052',
        'south' => 'http://south.internal:5052',
    ]);
    Http::fake([
        'http://north.internal:5052/shell/runs/'.SHELL_RUN_ID => Http::response(['message' => 'Not found.'], 404),
        'http://south.internal:5052/shell/runs/'.SHELL_RUN_ID => Http::response([
            'id' => SHELL_RUN_ID,
            'hook' => 'deploy',
            'started_at' => '2026-10-03T12:00:00Z',
            'duration_ms' => 5,
            'exit_code' => 1,
            'failed' => true,
        ]),
    ]);

    $this->withToken('registry-secret')->getJson('/shell/runs/'.SHELL_RUN_ID)->assertOk()
        ->assertJsonPath('data.node', 'south');
    $this->withToken('registry-secret')->getJson('/shell/runs/not-an-id')->assertNotFound();
    Http::assertSentCount(2);
});

it('fails closed when a node is unavailable or sends an invalid record', function (): void {
    config()->set('gateway.delivery_admin_nodes', [
        'north' => 'http://north.internal:5052',
        'south' => 'http://south.internal:5052',
    ]);
    Http::fake([
        'http://north.internal:5052/shell/runs/'.SHELL_RUN_ID => Http::response(['message' => 'Not found.'], 404),
        'http://south.internal:5052/shell/runs/'.SHELL_RUN_ID => Http::response(['message' => 'Unavailable.'], 503),
    ]);
    $this->withToken('registry-secret')->getJson('/shell/runs/'.SHELL_RUN_ID)->assertServiceUnavailable();

    config()->set('gateway.delivery_admin_nodes', null);
    Http::fake(['http://spinner.internal:5052/shell/runs/'.SHELL_RUN_ID => Http::response(['id' => 'wrong'])]);
    $this->withToken('registry-secret')->getJson('/shell/runs/'.SHELL_RUN_ID)->assertServiceUnavailable();
});
