<?php

declare(strict_types=1);

use Webong\Gateway\Automation\CapabilityAwareRoutePlanner;
use Webong\Gateway\Automation\InMemoryAutomationLanguageRegistry;
use Webong\Gateway\Contracts\AutomationCapability;
use Webong\Gateway\Contracts\AutomationExecutionTarget;
use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Protocols\Automation;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\RoutePlan;

it('tracks language capabilities without knowing language implementations', function (): void {
    $registry = new InMemoryAutomationLanguageRegistry;
    $registry->register('route-only', AutomationExecutionTarget::PLANNER, AutomationCapability::ROUTE);
    $registry->register(
        'scheduled',
        AutomationExecutionTarget::SPINNER,
        AutomationCapability::ROUTE,
        AutomationCapability::SCHEDULE,
    );

    expect($registry->supports('route-only', AutomationCapability::ROUTE))->toBeTrue()
        ->and($registry->supports('route-only', AutomationCapability::SCHEDULE))->toBeFalse()
        ->and($registry->target('route-only'))->toBe(AutomationExecutionTarget::PLANNER)
        ->and($registry->target('scheduled'))->toBe(AutomationExecutionTarget::SPINNER)
        ->and($registry->languages(AutomationCapability::ROUTE))->toBe(['route-only', 'scheduled'])
        ->and($registry->languages(AutomationCapability::SCHEDULE))->toBe(['scheduled']);

    expect(fn () => $registry->register(
        'scheduled',
        AutomationExecutionTarget::PLANNER,
        AutomationCapability::ROUTE,
    ))->toThrow(
        InvalidArgumentException::class,
        'Gateway automation language [scheduled] cannot use multiple execution targets.',
    );
});

it('requires Spinner to advertise an installed Go language', function (): void {
    $planner = new class implements RoutePlanner
    {
        public function plan(IngressRequest $request): RoutePlan
        {
            return RoutePlan::automate(new Automation('go-script', 'run()'));
        }
    };
    $languages = new InMemoryAutomationLanguageRegistry;
    $languages->register(
        'go-script',
        AutomationExecutionTarget::SPINNER,
        AutomationCapability::ROUTE,
    );
    $guarded = new CapabilityAwareRoutePlanner($planner, $languages);

    $request = new IngressRequest(
        deliveryId: 'delivery-1',
        method: 'POST',
        host: 'hooks.example.test',
        path: '/incoming',
        rawQuery: '',
        headers: [],
        body: '',
    );
    expect(fn () => $guarded->plan($request))->toThrow(
        RuntimeException::class,
        'Gateway automation language [go-script] is not available in this Spinner runtime.',
    );

    expect($guarded->plan(new IngressRequest(
        deliveryId: 'delivery-2',
        method: 'POST',
        host: 'hooks.example.test',
        path: '/incoming',
        rawQuery: '',
        headers: [],
        body: '',
        automationLanguages: ['go-script'],
    ))->action)->toBe(RoutePlan::AUTOMATION);
});

it('requires a Planner extension to consume its own language before the bridge', function (): void {
    $planner = new class implements RoutePlanner
    {
        public function plan(IngressRequest $request): RoutePlan
        {
            return RoutePlan::automate(new Automation('planner-script', 'run()'));
        }
    };
    $languages = new InMemoryAutomationLanguageRegistry;
    $languages->register(
        'planner-script',
        AutomationExecutionTarget::PLANNER,
        AutomationCapability::ROUTE,
    );

    expect(fn () => (new CapabilityAwareRoutePlanner($planner, $languages))->plan(new IngressRequest(
        deliveryId: 'delivery-1',
        method: 'POST',
        host: 'hooks.example.test',
        path: '/incoming',
        rawQuery: '',
        headers: [],
        body: '',
    )))->toThrow(
        RuntimeException::class,
        'Planner automation language [planner-script] was not consumed by its extension.',
    );
});

it('fails closed when a planner selects a language without route capability', function (): void {
    $planner = new class implements RoutePlanner
    {
        public function plan(IngressRequest $request): RoutePlan
        {
            return RoutePlan::automate(new Automation('disabled-script', 'run()'));
        }
    };
    $guarded = new CapabilityAwareRoutePlanner(
        planner: $planner,
        languages: new InMemoryAutomationLanguageRegistry,
    );
    $request = new IngressRequest(
        deliveryId: 'delivery-1',
        method: 'POST',
        host: 'hooks.example.test',
        path: '/incoming',
        rawQuery: '',
        headers: [],
        body: '',
    );

    expect(fn () => $guarded->plan($request))->toThrow(
        RuntimeException::class,
        'Gateway automation language [disabled-script] is not enabled for route plans.',
    );
});
