<?php

declare(strict_types=1);

namespace Webong\Gateway\Tests\Fixtures;

use Webong\Gateway\Contract\PathResolver;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\PathBinding;

final class HttpSmokePathResolver implements PathResolver
{
    public function resolve(IngressRequest $request): ?PathBinding
    {
        if ($request->path !== '/http-smoke/registry-ingress') {
            return null;
        }

        return new PathBinding(
            endpointKey: 'http-smoke-endpoint',
            scope: 'application',
            key: 'app-123',
        );
    }
}
