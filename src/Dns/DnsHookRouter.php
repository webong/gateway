<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use Webong\WebProxy\Contracts\Router;
use Webong\WebProxy\Models\WebProxyCall;

/**
 * DNS queries are normalized by Gateway before they reach WebProxy. This
 * router exists only to give DNS-owned endpoints a registered WebProxy client.
 */
final class DnsHookRouter implements Router
{
    public function routes(WebProxyCall $webhookCall): iterable
    {
        return [];
    }
}
