<?php

declare(strict_types=1);

use Webong\Gateway\Automation\CapabilityAwareRoutePlanner;
use Webong\Gateway\Contracts\AutomationCapability;
use Webong\Gateway\Contracts\AutomationExecutionTarget;
use Webong\Gateway\Contracts\AutomationLanguageRegistry;
use Webong\Gateway\Contracts\CompanionActionRegistry;
use Webong\Gateway\HookScript\HookScriptCompanionAction;
use Webong\Gateway\HookScript\HookScriptLanguage;
use Webong\Gateway\Protocols\CompanionActionInvocation;
use Webong\Gateway\Protocols\CompanionActionResult;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\RoutePlan;

it('registers HookScript as a Spinner language backed by one PHP companion', function (): void {
    $languages = app(AutomationLanguageRegistry::class);
    $companions = app(CompanionActionRegistry::class);

    expect($languages->target(HookScriptLanguage::ID))->toBe(AutomationExecutionTarget::SPINNER)
        ->and($languages->supports(HookScriptLanguage::ID, AutomationCapability::ROUTE))->toBeTrue()
        ->and($languages->supports(HookScriptLanguage::ID, AutomationCapability::SCHEDULE))->toBeTrue()
        ->and($languages->supports(HookScriptLanguage::ID, AutomationCapability::CREDENTIAL))->toBeTrue()
        ->and($companions->capability(HookScriptCompanionAction::NAME)?->version)
        ->toBe(HookScriptCompanionAction::VERSION);

    $plan = app(CapabilityAwareRoutePlanner::class)->plan(new IngressRequest(
        deliveryId: 'delivery-1',
        method: 'POST',
        host: 'hooks.example.test',
        path: '/incoming',
        rawQuery: '',
        headers: [],
        body: 'payload',
        automationLanguages: [HookScriptLanguage::ID],
    ));

    expect($plan->action)->toBe(RoutePlan::AUTOMATION)
        ->and($plan->automation?->language)->toBe(HookScriptLanguage::ID)
        ->and($plan->automation?->source)->toContain("respond('scripted', 202")
        ->and($plan->metadata)->toBe(['planner' => 'fixture']);
});

it('executes HookScript through the registered companion action', function (): void {
    $invocation = new CompanionActionInvocation(
        action: HookScriptCompanionAction::NAME,
        executionId: 'hookscript-execution-1',
        idempotencyKey: 'hookscript:delivery-1',
        input: [
            'source' => <<<'PRIMI'
                forward('https://subscriber.example.test/hook', var('request.content'), 'POST');
                schedule([
                    'key': 'follow-up', 'name': 'Follow up', 'cron': '0 9 * * *',
                    'language': 'javascript', 'source': "gateway.respond('ok', 200)"
                ]);
                respond('accepted', 202, ['X-Engine: Primi']);
                PRIMI,
            'event' => [
                'delivery_id' => 'delivery-1',
                'method' => 'POST',
                'host' => 'hooks.example.test',
                'path' => '/incoming',
                'raw_query' => '',
                'headers' => ['Content-Type' => ['text/plain']],
                'body' => 'payload',
            ],
            'variables' => [],
        ],
        deadline: new DateTimeImmutable('+30 seconds'),
        companionVersion: HookScriptCompanionAction::VERSION,
    );

    $result = app(CompanionActionRegistry::class)->invoke($invocation);

    expect($result->status)->toBe(CompanionActionResult::SUCCEEDED)
        ->and($result->output['outcome']['response'])->toBe([
            'status' => 202,
            'headers' => ['X-Engine' => ['Primi']],
            'body' => 'accepted',
        ])
        ->and($result->output['outcome']['deliveries'])->toHaveCount(1)
        ->and($result->output['outcome']['deliveries'][0]['body'])->toBe('payload')
        ->and($result->output['outcome']['schedules'])->toHaveCount(1)
        ->and($result->output['outcome']['schedules'][0]['key'])->toBe('follow-up');
});
