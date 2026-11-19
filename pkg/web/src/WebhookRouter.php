<?php

declare(strict_types=1);

namespace Webong\Gateway\Web;

use Webong\Proxy\Contracts\Router;
use Webong\Proxy\Models\WebProxyCall;

/** Reserves Gateway's webhook client while ingress planning selects destinations. */
final class WebhookRouter implements Router
{
    public function routes(WebProxyCall $webhookCall): iterable
    {
        return [];
    }
}
