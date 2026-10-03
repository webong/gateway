<?php

declare(strict_types=1);

namespace Webong\Gateway\Events;

use Webong\Gateway\Events\Models\GatewayEndpointEvent;

final class EventRetention
{
    public function maybePrune(): void
    {
        if (random_int(1, 1000) === 1) {
            $this->prune();
        }
    }

    public function prune(): int
    {
        $days = max(1, (int) config('gateway.events.retention_days', 30));

        return GatewayEndpointEvent::query()->where('received_at', '<', now()->subDays($days))->delete();
    }
}
