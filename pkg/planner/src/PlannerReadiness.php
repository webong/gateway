<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Throwable;
use Webong\Proxy\Models\WebProxyEndpoint;

final class PlannerReadiness
{
    public function ready(): bool
    {
        try {
            // An empty registry is ready; an unavailable or unmigrated database is not.
            WebProxyEndpoint::query()->exists();

            return true;
        } catch (Throwable) {
            return false;
        }
    }
}
