<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Webong\Proxy\Contracts\Router;
use Webong\Proxy\Models\WebProxyCall;

/**
 * Gateway normalizes ingress before WebProxy selects destinations. This client
 * reserves one WebProxy namespace for Gateway-owned endpoint identities.
 */
final class GatewayRouter implements Router
{
    public function routes(WebProxyCall $webhookCall): iterable
    {
        return [];
    }
}
