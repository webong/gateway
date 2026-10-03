<?php

declare(strict_types=1);

namespace Webong\Gateway\Automation;

use RuntimeException;
use Webong\Gateway\Contracts\AutomationCapability;
use Webong\Gateway\Contracts\AutomationExecutionTarget;
use Webong\Gateway\Contracts\AutomationLanguageRegistry;
use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Events\WebEventRecorder;
use Webong\Gateway\Protocol\IngressRequest;
use Webong\Gateway\Protocol\RoutePlan;

final readonly class CapabilityAwareRoutePlanner implements RoutePlanner
{
    public function __construct(
        private RoutePlanner $planner,
        private AutomationLanguageRegistry $languages,
        private ?WebEventRecorder $events = null,
    ) {}

    public function plan(IngressRequest $request): RoutePlan
    {
        $plan = $this->planner->plan($request);
        $language = $plan->automation?->language;

        if ($plan->action === RoutePlan::AUTOMATION
            && ($language === null || ! $this->languages->supports($language, AutomationCapability::ROUTE))) {
            throw new RuntimeException("Gateway automation language [{$language}] is not enabled for route plans.");
        }

        if ($language !== null
            && $this->languages->target($language) === AutomationExecutionTarget::SPINNER
            && ! in_array($language, $request->automationLanguages, true)) {
            throw new RuntimeException("Gateway automation language [{$language}] is not available in this Spinner runtime.");
        }

        if ($language !== null
            && $this->languages->target($language) === AutomationExecutionTarget::PLANNER) {
            throw new RuntimeException("Planner automation language [{$language}] was not consumed by its extension.");
        }

        $this->events?->record($request, $plan);

        return $plan;
    }
}
