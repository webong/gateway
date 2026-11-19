<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel;

use Webong\Proxy\Contracts\Router;
use Webong\Proxy\Models\WebProxyCall;

/**
 * Empty Proxy router retained for non-Web extensions and cached configurations.
 * Gateway-specific webhook registration belongs to the Web extension.
 */
final class GatewayRouter implements Router
{
    public function routes(WebProxyCall $webhookCall): iterable
    {
        return [];
    }
}
