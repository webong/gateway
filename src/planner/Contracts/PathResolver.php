<?php

declare(strict_types=1);

namespace Webong\Gateway\Contracts;

use Webong\Gateway\Protocol\IngressRequest;
use Webong\Gateway\Protocol\PathBinding;

/**
 * Resolves the application-owned public path into registry routing data.
 *
 * This is intentionally an application contract: paths may come from Laravel
 * routes, a database, a signed registration, or a provider-specific matcher.
 */
interface PathResolver
{
    public function resolve(IngressRequest $request): ?PathBinding;
}
