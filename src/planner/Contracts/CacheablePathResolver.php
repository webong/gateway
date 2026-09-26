<?php

declare(strict_types=1);

namespace Webong\Gateway\Contracts;

use Webong\Gateway\Protocol\IngressRequest;

/**
 * Opts an application path resolver into path-binding caching.
 *
 * The returned key must include every request attribute that can change the
 * binding. A typical path-only resolver uses method, host, and path.
 */
interface CacheablePathResolver extends PathResolver
{
    public function cacheKey(IngressRequest $request): string;
}
