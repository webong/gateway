<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use LogicException;
use Webong\Gateway\Contracts\CacheablePathResolver;
use Webong\Gateway\Protocols\IngressRequest;

final class CacheableMailHookPathResolver extends MailHookPathResolver implements CacheablePathResolver
{
    public function cacheKey(IngressRequest $request): string
    {
        if ($request->protocol === 'smtp') {
            return 'smtp|'.strtolower($request->path);
        }
        if ($this->fallback === null) { return 'fallback|none'; }
        if (! $this->fallback instanceof CacheablePathResolver) {
            throw new LogicException('A cacheable mail hook resolver requires a cacheable fallback.');
        }

        return 'fallback|'.$this->fallback->cacheKey($request);
    }
}
