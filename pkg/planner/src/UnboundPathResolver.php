<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Webong\Gateway\Contracts\PathResolver;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\PathBinding;

/** Allows an HTTP-only Planner to pass unbound paths to its host application. */
final class UnboundPathResolver implements PathResolver
{
    public function resolve(IngressRequest $request): ?PathBinding { return null; }
}
