<?php

declare(strict_types=1);

namespace Webong\WebRelay\Tests\Fixtures;

use Webong\WebRelay\Contracts\PathResolver;
use Webong\WebRelay\Protocol\IngressRequest;
use Webong\WebRelay\Protocol\PathBinding;

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
