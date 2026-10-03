<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use LogicException;
use Webong\Gateway\Contracts\CacheablePathResolver;
use Webong\Gateway\Protocol\IngressRequest;

final class CacheableDnsHookPathResolver extends DnsHookPathResolver implements CacheablePathResolver
{
    public function cacheKey(IngressRequest $request): string
    {
        if ($request->protocol === 'dns') {
            $zone = strtolower(rtrim((string) ($request->attributes['zone'] ?? ''), '.'));
            $token = strtolower((string) ($request->attributes['endpoint'] ?? ltrim($request->path, '/')));

            return 'dns|'.$zone.'|'.$token;
        }

        if ($this->fallback === null) {
            return 'fallback|none';
        }
        if (! $this->fallback instanceof CacheablePathResolver) {
            throw new LogicException('A cacheable DNS hook resolver requires a cacheable fallback.');
        }

        return 'fallback|'.$this->fallback->cacheKey($request);
    }
}
