<?php

declare(strict_types=1);

namespace Gateway\Router;

use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Protocols\Automation;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\RoutePlan;

/**
 * Benchmark-only Planner route planner.
 *
 * It maps two fixed ingress paths to two reviewed HookScript sources so the
 * benchmark exercises the real Spinner guest -> private Planner connection ->
 * PHP companion path. It is never part of a product image; the harness mounts
 * it into the local Planner container.
 */
final class BenchmarkPlanner implements RoutePlanner
{
    private const array WORKLOADS = [
        '/hooks/bench-small' => 'small',
        '/hooks/bench-real' => 'real',
    ];

    public function __construct(
        private readonly string $sourceDirectory = '/bench/sources',
    ) {
    }

    public function plan(IngressRequest $request): RoutePlan
    {
        $workload = self::WORKLOADS[$request->path] ?? null;
        if ($workload === null) {
            return RoutePlan::passThrough();
        }

        $source = file_get_contents($this->sourceDirectory.'/'.$workload.'.primi');
        if ($source === false) {
            throw new \RuntimeException('Benchmark workload source is unavailable.');
        }

        return RoutePlan::automate(
            new Automation(language: 'webhookscript', source: $source),
            metadata: ['benchmark_workload' => $workload],
        );
    }
}
