<?php

declare(strict_types=1);

namespace Webong\Gateway\HookScript;

use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Contracts\ScriptScheduleRegistrar;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\Response;
use Webong\Gateway\Protocols\RoutePlan;

/** Executes this extension's language in PHP and passes other plans through. */
final readonly class PrimiRoutePlanner implements RoutePlanner
{
    public function __construct(
        private RoutePlanner $planner,
        private HookScriptRuntime $runtime,
        private ?ScriptScheduleRegistrar $schedules = null,
    ) {
    }

    public function plan(IngressRequest $request): RoutePlan
    {
        $plan = $this->planner->plan($request);
        if ($plan->action !== RoutePlan::AUTOMATION || $plan->automation?->language !== HookScriptLanguage::ID) {
            return $plan;
        }

        try {
            $outcome = $this->runtime->execute($plan->automation->source, $request);
            if ($outcome->schedules !== []) {
                if ($this->schedules === null) {
                    throw new \RuntimeException('Script schedule registration is not configured.');
                }
                $this->schedules->register($this->origin($request, $plan), ...$outcome->schedules);
            }
        } catch (\Throwable) {
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

    private function origin(IngressRequest $request, RoutePlan $plan): string
    {
        $endpoint = $plan->metadata['endpoint_id'] ?? null;
        if (is_string($endpoint) && $endpoint !== '') {
            return 'endpoint:'.$endpoint;
        }

        return 'route:'.hash('sha256', $request->host."\n".$request->path);
    }
}
