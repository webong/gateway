<?php

declare(strict_types=1);

namespace Webong\WebRelay\Tests\Support;

use Webong\WebRelay\Contracts\PathResolver;
use Webong\WebRelay\Protocol\IngressRequest;
use Webong\WebRelay\Protocol\PathBinding;

final class TestPathResolver implements PathResolver
{
    public static string $endpointKey = 'relay-endpoint';

    public function resolve(IngressRequest $request): ?PathBinding
    {
        if ($request->path !== '/provider/events/app-123') {
            return null;
        }

        return new PathBinding(
            endpointKey: self::$endpointKey,
            scope: 'application',
            key: 'app-123',
        );
    }
}
