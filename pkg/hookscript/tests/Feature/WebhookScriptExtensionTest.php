<?php

declare(strict_types=1);

use Webong\Gateway\Automation\CapabilityAwareRoutePlanner;
use Webong\Gateway\Contracts\AutomationCapability;
use Webong\Gateway\Contracts\AutomationExecutionTarget;
use Webong\Gateway\Contracts\AutomationLanguageRegistry;
use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Contracts\ScriptScheduleRegistrar;
use Webong\Gateway\Protocols\Automation;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\RoutePlan;
use Webong\Gateway\Protocols\ScheduleCommand;
use Webong\Gateway\HookScript\PrimiRoutePlanner;
use Webong\Gateway\HookScript\HookScriptLanguage;
use Webong\Gateway\HookScript\HookScriptRuntime;

it('decorates the configured planner only when the extension provider is installed', function (): void {
    $planner = app(RoutePlanner::class);

    expect(app(AutomationLanguageRegistry::class)->supports(
        HookScriptLanguage::ID,
        AutomationCapability::ROUTE,
    ))->toBeTrue()
        ->and(app(AutomationLanguageRegistry::class)->target(HookScriptLanguage::ID))
        ->toBe(AutomationExecutionTarget::PLANNER)
        ->and(app(AutomationLanguageRegistry::class)->supports(
            HookScriptLanguage::ID,
            AutomationCapability::SCHEDULE,
        ))->toBeFalse()
        ->and(app(AutomationLanguageRegistry::class)->supports(
            HookScriptLanguage::ID,
            AutomationCapability::CREDENTIAL,
        ))->toBeTrue();

    expect($planner)->toBeInstanceOf(PrimiRoutePlanner::class);

    $plan = app(CapabilityAwareRoutePlanner::class)->plan(new IngressRequest(
        deliveryId: 'delivery-1',
        method: 'POST',
        host: 'hooks.example.test',
        path: '/incoming',
        rawQuery: '',
        headers: [],
        body: 'payload',
    ));

    expect($plan->action)->toBe(RoutePlan::RELAY)
        ->and($plan->automation)->toBeNull()
        ->and($plan->relays)->toHaveCount(1)
        ->and($plan->relays[0]->url)->toBe('https://subscriber.example.test/hook')
        ->and($plan->immediateResponse?->statusCode)->toBe(202)
        ->and($plan->immediateResponse?->body)->toBe('scripted')
        ->and($plan->metadata)->toBe(['planner' => 'fixture']);
});

it('persists emitted schedules through the Planner contract', function (): void {
    $registrar = new class implements ScriptScheduleRegistrar
    {
        public string $origin = '';

        /** @var list<ScheduleCommand> */
        public array $schedules = [];

        public function register(string $origin, ScheduleCommand ...$schedules): void
        {
            $this->origin = $origin;
            $this->schedules = $schedules;
        }
    };
    $planner = new class implements RoutePlanner
    {
        public function plan(IngressRequest $request): RoutePlan
        {
            return RoutePlan::automate(
                new Automation(HookScriptLanguage::ID, <<<'PRIMI'
                    schedule([
                        'key': 'daily', 'name': 'Daily', 'cron': '0 9 * * *',
                        'language': 'javascript', 'source': "gateway.respond('ok', 200)"
                    ]);
                    PRIMI),
                metadata: ['endpoint_id' => 'endpoint-1'],
            );
        }
    };

    $plan = (new PrimiRoutePlanner($planner, new HookScriptRuntime, $registrar))->plan(
        new IngressRequest('delivery-1', 'POST', 'hooks.example.test', '/incoming', '', [], ''),
    );

    expect($plan->immediateResponse?->statusCode)->toBe(204)
        ->and($registrar->origin)->toBe('endpoint:endpoint-1')
        ->and($registrar->schedules)->toHaveCount(1)
        ->and($registrar->schedules[0]->key)->toBe('daily');
});
