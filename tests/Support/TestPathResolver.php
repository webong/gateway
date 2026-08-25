<?php

declare(strict_types=1);

namespace Webong\NetGateway\Tests\Support;

use Webong\NetGateway\Contracts\CacheablePathResolver;
use Webong\NetGateway\Protocol\IngressRequest;
use Webong\NetGateway\Protocol\PathBinding;

final class TestPathResolver implements CacheablePathResolver
{
    public static string $endpointKey = 'relay-endpoint';

    public static int $resolutions = 0;

    public function resolve(IngressRequest $request): ?PathBinding
    {
        self::$resolutions++;

        if ($request->path !== '/provider/events/app-123') {
            return null;
        }

        return new PathBinding(
            endpointKey: self::$endpointKey,
            scope: 'application',
            key: 'app-123',
        );
    }

    public function cacheKey(IngressRequest $request): string
    {
        return implode('|', [$request->method, $request->host, $request->path]);
    }
}
