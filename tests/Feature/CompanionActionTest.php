<?php

declare(strict_types=1);

use Illuminate\Support\Facades\DB;
use Webong\Gateway\Contracts\CompanionActionRegistry;
use Webong\Gateway\Protocols\CompanionActionInvocation;
use Webong\Gateway\Tests\Fixtures\CreateTicketCompanionAction;

beforeEach(function (): void {
    CreateTicketCompanionAction::$calls = 0;
    app(CompanionActionRegistry::class)->register(
        'email.create-ticket',
        CreateTicketCompanionAction::class,
        version: '1.0.0',
    );
});

function companionInvocation(array $replace = []): array
{
    return array_replace([
        'protocol_version' => CompanionActionInvocation::VERSION,
        'action' => 'email.create-ticket',
        'companion_version' => '1.0.0',
        'execution_id' => 'execution-1',
        'idempotency_key' => 'message-1',
        'input' => ['subject' => 'Help'],
    ], $replace);
}

it('hides the private companion action interface without Spinner authentication', function (): void {
    $this->getJson('/_internal/gateway/companion-actions')->assertNotFound();
    $this->postJson('/_internal/gateway/companion-actions/email.create-ticket/executions', companionInvocation())
        ->assertNotFound();
});

it('advertises installed companion capabilities and executes typed input', function (): void {
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->getJson('/_internal/gateway/companion-actions')
        ->assertOk()
        ->assertHeader('Cache-Control', 'no-store, private')
        ->assertJson([
            'protocol_version' => 'v1',
            'actions' => [[
                'name' => 'email.create-ticket',
                'version' => '1.0.0',
            ]],
        ]);

    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/companion-actions/email.create-ticket/executions', companionInvocation())
        ->assertOk()
        ->assertJsonPath('protocol_version', 'v1')
        ->assertJsonPath('execution_id', 'execution-1')
        ->assertJsonPath('status', 'succeeded')
        ->assertJsonPath('output.ticket_id', 'ticket-1')
        ->assertJsonPath('output.subject', 'Help');

    expect(CreateTicketCompanionAction::$calls)->toBe(1)
        ->and(DB::table('companion_action_executions')->where('execution_id', 'execution-1')->value('status'))
        ->toBe('succeeded');
});

it('replays a completed idempotent execution without running PHP again', function (): void {
    $endpoint = '/_internal/gateway/companion-actions/email.create-ticket/executions';
    $this->withHeader('X-Gateway-Internal', 'internal-secret')->postJson($endpoint, companionInvocation())->assertOk();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')->postJson($endpoint, companionInvocation([
        'execution_id' => 'execution-2',
    ]))->assertOk()
        ->assertJsonPath('execution_id', 'execution-1')
        ->assertJsonPath('output.ticket_id', 'ticket-1');

    expect(CreateTicketCompanionAction::$calls)->toBe(1)
        ->and(DB::table('companion_action_executions')->count())->toBe(1);
});

it('rejects incompatible companions and idempotency-key reuse with different input', function (): void {
    $endpoint = '/_internal/gateway/companion-actions/email.create-ticket/executions';
    $this->withHeader('X-Gateway-Internal', 'internal-secret')->postJson($endpoint, companionInvocation([
        'companion_version' => '2.0.0',
    ]))->assertConflict();
    expect(DB::table('companion_action_executions')->count())->toBe(0);

    $this->withHeader('X-Gateway-Internal', 'internal-secret')->postJson($endpoint, companionInvocation())->assertOk();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')->postJson($endpoint, companionInvocation([
        'execution_id' => 'execution-2',
        'input' => ['subject' => 'Different'],
    ]))->assertConflict();

    expect(CreateTicketCompanionAction::$calls)->toBe(1);
});

it('reports an already claimed execution without running it twice', function (): void {
    $invocation = CompanionActionInvocation::fromArray(companionInvocation());
    DB::table('companion_action_executions')->insert([
        'execution_id' => 'execution-1',
        'action' => 'email.create-ticket',
        'companion_version' => '1.0.0',
        'companion_digest' => null,
        'idempotency_hash' => hash('sha256', 'message-1'),
        'request_hash' => $invocation->fingerprint(),
        'status' => 'running',
        'result' => null,
        'started_at' => now('UTC'),
        'completed_at' => null,
        'created_at' => now('UTC'),
        'updated_at' => now('UTC'),
    ]);

    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/companion-actions/email.create-ticket/executions', companionInvocation())
        ->assertAccepted()
        ->assertJsonPath('execution_id', 'execution-1')
        ->assertJsonPath('status', 'in_progress');

    expect(CreateTicketCompanionAction::$calls)->toBe(0);
});

it('does not expose raw PHP installation through the invocation interface', function (): void {
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/companion-actions/email.create-ticket/executions', companionInvocation([
            'php' => '<?php file_put_contents("/tmp/owned", "yes");',
        ]))
        ->assertUnprocessable();

    expect(CreateTicketCompanionAction::$calls)->toBe(0);
});
