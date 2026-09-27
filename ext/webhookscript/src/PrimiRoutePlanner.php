<?php

declare(strict_types=1);

namespace Webong\Gateway\WebhookScript;

use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Protocol\IngressRequest;
use Webong\Gateway\Protocol\Response;
use Webong\Gateway\Protocol\RoutePlan;

/** Executes this extension's language in PHP and passes other plans through. */
final readonly class PrimiRoutePlanner implements RoutePlanner
{
    public function __construct(
        private RoutePlanner $planner,
        private WebhookScriptRuntime $runtime,
    ) {
    }

    public function plan(IngressRequest $request): RoutePlan
    {
        $plan = $this->planner->plan($request);
        if ($plan->action !== RoutePlan::AUTOMATION || $plan->automation?->language !== WebhookScriptLanguage::ID) {
            return $plan;
        }

        try {
            $outcome = $this->runtime->execute($plan->automation->source, $request);
        } catch (WebhookScriptExecutionException) {
            return RoutePlan::respond(
                new Response(statusCode: 502, body: 'Automation execution failed.'),
                metadata: $plan->metadata,
            );
        }

        $relays = [...$plan->relays, ...$outcome->deliveries];
        if ($outcome->response !== null && $relays === []) {
            return RoutePlan::respond($outcome->response, metadata: $plan->metadata);
        }
        if ($relays !== []) {
            return RoutePlan::relay(
                relays: $relays,
                immediateResponse: $outcome->response,
                metadata: $plan->metadata,
            );
        }

        return RoutePlan::respond(new Response(statusCode: 204), metadata: $plan->metadata);
    }
}
