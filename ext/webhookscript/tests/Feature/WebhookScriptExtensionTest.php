<?php

declare(strict_types=1);

use Webong\Gateway\Automation\CapabilityAwareRoutePlanner;
use Webong\Gateway\Contracts\AutomationCapability;
use Webong\Gateway\Contracts\AutomationExecutionTarget;
use Webong\Gateway\Contracts\AutomationLanguageRegistry;
use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Protocol\IngressRequest;
use Webong\Gateway\Protocol\RoutePlan;
use Webong\Gateway\WebhookScript\PrimiRoutePlanner;
use Webong\Gateway\WebhookScript\WebhookScriptLanguage;

it('decorates the configured planner only when the extension provider is installed', function (): void {
    $planner = app(RoutePlanner::class);

    expect(app(AutomationLanguageRegistry::class)->supports(
        WebhookScriptLanguage::ID,
        AutomationCapability::ROUTE,
    ))->toBeTrue()
        ->and(app(AutomationLanguageRegistry::class)->target(WebhookScriptLanguage::ID))
        ->toBe(AutomationExecutionTarget::PLANNER)
        ->and(app(AutomationLanguageRegistry::class)->supports(
            WebhookScriptLanguage::ID,
            AutomationCapability::SCHEDULE,
        ))->toBeFalse();

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
