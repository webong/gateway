<?php

declare(strict_types=1);

namespace Gateway\Router;

use Webong\Gateway\Contracts\PathResolver;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\PathBinding;

/**
 * Benchmark/verification path resolver for the Planner container image.
 *
 * The production image resolves `/hooks/{endpointKey}` only. The benchmark sends
 * `/http-smoke/registry-ingress`, which the test suite's HttpSmokePathResolver
 * binds to the seeded `http-smoke-endpoint`; this class repeats that binding so the
 * same request works against the image. The harness bind-mounts it read-only into
 * `/gateway/app/helpers/BenchHttpSmokePathResolver.php` (the `Gateway\Router\` PSR-4 root),
 * so the image itself is unchanged.
 *
 * `/http-smoke/hold` additionally holds the request open for
 * GATEWAY_BENCH_HOLD_SECONDS, and is only used by the container shutdown check
 * (`--check-shutdown`). GATEWAY_BENCH_HOLD_ENTERED_FILE is written once the
 * request is inside the handler. The hold is time-based because a file written by
 * the host is not reliably visible to a container through a bind mount.
 */
final class BenchHttpSmokePathResolver implements PathResolver
{
    public function resolve(IngressRequest $request): ?PathBinding
    {
        if ($request->path === '/http-smoke/hold') {
            self::hold();
        } elseif ($request->path !== '/http-smoke/registry-ingress') {
            return null;
        }

        return new PathBinding(
            endpointKey: 'http-smoke-endpoint',
            scope: 'application',
            key: 'app-123',
        );
    }

    private static function hold(): void
    {
        $entered = getenv('GATEWAY_BENCH_HOLD_ENTERED_FILE');
        if (is_string($entered) && $entered !== '') {
            file_put_contents($entered, (string) getmypid());
        }

        // Short sleeps until the deadline: a single long usleep() returns early when a
        // signal (the stop signal this hold exists to outlast) is delivered.
        $until = microtime(true) + max(0.0, min(60.0, (float) getenv('GATEWAY_BENCH_HOLD_SECONDS')));
        while (microtime(true) < $until) {
            usleep(10_000);
        }
    }
}
