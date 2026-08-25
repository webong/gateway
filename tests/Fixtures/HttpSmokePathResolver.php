<?php

declare(strict_types=1);

namespace Webong\NetGateway\Tests\Fixtures;

use Webong\NetGateway\Contracts\PathResolver;
use Webong\NetGateway\Protocol\IngressRequest;
use Webong\NetGateway\Protocol\PathBinding;

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
