<?php

declare(strict_types=1);

namespace Webong\Gateway\Reverb;

use RuntimeException;
use Webong\Gateway\Reverb\Models\ReverbServer;

final readonly class ReverbServerContext
{
    public function id(): string
    {
        $serverId = config('gateway-reverb.server_id');

        if (! is_string($serverId) || trim($serverId) === '') {
            throw new RuntimeException('GATEWAY_REVERB_SERVER_ID is required for the Gateway Reverb application provider.');
        }

        if (! ReverbServer::query()->whereKey($serverId)->exists()) {
            throw new RuntimeException("Gateway Reverb server [{$serverId}] does not exist.");
        }

        return $serverId;
    }
}
