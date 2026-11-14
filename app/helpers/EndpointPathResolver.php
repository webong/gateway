<?php

declare(strict_types=1);

namespace Gateway\Router;

use Webong\Gateway\Contracts\PathResolver;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\PathBinding;

final class EndpointPathResolver implements PathResolver
{
    public function resolve(IngressRequest $request): ?PathBinding
    {
        if (! preg_match('~^/hooks/([A-Za-z0-9_.-]+)$~D', $request->path, $matches)) {
            return null;
        }

        return new PathBinding(
            endpointKey: $matches[1],
            scope: 'path',
            key: $request->path,
        );
    }
}
