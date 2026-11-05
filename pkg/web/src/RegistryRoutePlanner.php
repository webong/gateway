<?php

declare(strict_types=1);

namespace Webong\Gateway\Web;

use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\RoutePlan;

/** Webhook route adapter with Web-owned observation identity. */
final class RegistryRoutePlanner extends \Webong\Gateway\Laravel\RegistryRoutePlanner
{
    public function plan(IngressRequest $request): RoutePlan
    {
        $plan = parent::plan($request);

        return new RoutePlan(
            action: $plan->action,
            immediateResponse: $plan->immediateResponse,
            reply: $plan->reply,
            relays: $plan->relays,
            automation: $plan->automation,
            metadata: [...$plan->metadata, 'route_kind' => 'webhook'],
            version: $plan->version,
            shellNotification: $plan->shellNotification,
        );
    }
}
