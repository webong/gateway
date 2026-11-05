#!/usr/bin/env php
<?php

declare(strict_types=1);

/**
 * Workerman Planner benchmark.
 *
 * Starts a real `app/workerman.php` process against a disposable SQLite database
 * that is migrated and seeded with a real endpoint + relay subscription, then
 * runs a representative authenticated, database-backed planning workload and
 * reports throughput, latency percentiles, and per-worker memory.
 *
 * A same-workload comparison against an existing Octane server (frankenphp /
 * swoole / roadrunner) is not yet automated here and remains OUTSTANDING; the
 * Planner container image and a PostgreSQL database are required (see REPORT).
 *
 * Nothing here touches an existing application database, and shutdown terminates
 * only the process this script launched. Failures exit non-zero after cleanup.
 */

ob_implicit_flush(true);

require __DIR__.'/../vendor/autoload.php';
require __DIR__.'/../tests/Support/WorkermanPlannerHelper.php';

$root = dirname(__DIR__);

$connections = (int) (getenv('GATEWAY_BENCH_CONNECTIONS') ?: 1000);
$warmup = (int) (getenv('GATEWAY_BENCH_WARMUP') ?: 50);
$workerCount = (int) (getenv('GATEWAY_BENCH_WORKERS') ?: 1);
$sampleEvery = max(1, (int) (getenv('GATEWAY_BENCH_SAMPLE_EVERY') ?: 50));

$endpointKey = 'http-smoke-endpoint';
$relayUrl = 'https://subscriber.example.test/hook';
$internalToken = 'gateway-internal-test-token';
$planBody = json_encode([
    'delivery_id' => 'dlr-bench',
    'method' => 'POST',
    'path' => '/http-smoke/registry-ingress',
    'body' => '',
    'headers' => [],
], JSON_THROW_ON_ERROR);

$percentile = function (array $times, float $p): float {
    if ($times === []) {
        return 0.0;
    }
    sort($times);
    $index = (int) ceil(($p / 100) * count($times)) - 1;
    $index = max(0, min($index, count($times) - 1));

    return $times[$index];
};

/** @param list<int> $workerPids @return list<int> RSS values in bytes. */
$sampleWorkerRss = function (array $workerPids): array {
    $rss = [];
    foreach ($workerPids as $pid) {
        $output = [];
        exec('ps -o rss= -p '.escapeshellarg((string) $pid).' 2>/dev/null', $output);
        if (! empty($output) && ctype_digit(trim($output[0]))) {
            $rss[] = (int) trim($output[0]) * 1024;
        }
    }

    return $rss;
};

$dataDirectory = gatewayPlannerDisposableDataDirectory();
$database = $dataDirectory.'/gateway.sqlite';
touch($database);

$baseEnv = gatewayPlannerBaseEnv($dataDirectory, $database);
$baseEnv['GATEWAY_INTERNAL_TOKEN'] = $internalToken;

/** @var array<string, mixed>|null $metrics */
$metrics = null;

try {
    gatewayPlannerWithEnvironment(
        $baseEnv,
        function () use (
            $baseEnv,
            $database,
            $dataDirectory,
            $endpointKey,
            $relayUrl,
            $internalToken,
            $planBody,
            $workerCount,
            $connections,
            $warmup,
            $sampleEvery,
            $percentile,
            $sampleWorkerRss,
            &$metrics,
        ): void {
            gatewayPlannerRunMigrations($database);
            gatewayPlannerSeedEndpoint($baseEnv, $endpointKey, 'application', 'app-123', $relayUrl, 'workspace-42');
            echo "migrations + seed applied\n";

            $port = gatewayPlannerFindFreePort();
            $handle = gatewayPlannerStartWorkerman($dataDirectory, $port, [
                'GATEWAY_PLANNER_WORKERS' => (string) $workerCount,
                'GATEWAY_PLANNER_MAX_REQUESTS' => '100000',
            ]);
            try {
                gatewayPlannerWaitForPort($port, 30.0);
                $masterPid = $handle['masterPid'];
                $uri = "http://127.0.0.1:$port/_internal/gateway/plan";
                $headers = ['Content-Type' => 'application/json', 'X-Gateway-Internal' => $internalToken];

                $plan = function () use ($uri, $headers, $planBody): array {
                    return gatewayPlannerRequest('POST', $uri, $headers, $planBody);
                };

                for ($i = 0; $i < $warmup; $i++) {
                    $plan();
                }
                $probe = $plan();
                $probePayload = json_decode($probe['body'], true, 512, JSON_THROW_ON_ERROR);
                if ($probe['status'] !== 200
                    || ($probePayload['action'] ?? null) !== 'relay'
                    || ($probePayload['relays'][0]['url'] ?? null) !== $relayUrl) {
                    throw new RuntimeException("representative plan response invalid (status {$probe['status']})");
                }
                echo "warmup done ({$warmup} requests), response validated\n";

                $latencies = [];
                $errors = 0;
                $rssSamples = 0;
                $peakWorkerRss = 0;
                $currentRss = [];
                $workstart = microtime(true);

                for ($i = 0; $i < $connections; $i++) {
                    $response = $plan();
                    if ($response['status'] !== 200) {
                        $errors++;
                        continue;
                    }
                    $payload = json_decode($response['body'], true, 512, JSON_THROW_ON_ERROR);
                    if (($payload['action'] ?? null) !== 'relay' || ($payload['relays'][0]['url'] ?? null) !== $relayUrl) {
                        $errors++;
                        continue;
                    }
                    $latencies[] = (float) ($response['elapsed'] ?? 0.0);

                    if ($i % $sampleEvery === 0) {
                        foreach ($sampleWorkerRss(gatewayPlannerWorkerPids($masterPid)) as $rss) {
                            $rssSamples++;
                            $peakWorkerRss = max($peakWorkerRss, $rss);
                        }
                    }
                }
                $workend = microtime(true);
                $currentRss = $sampleWorkerRss(gatewayPlannerWorkerPids($masterPid));

                $metrics = [
                    'connections' => $connections,
                    'successful' => count($latencies),
                    'errors' => $errors,
                    'totalTime' => $workend - $workstart,
                    'latencies' => $latencies,
                    'avgLatency' => count($latencies) ? array_sum($latencies) / count($latencies) : 0.0,
                    'p50' => $percentile($latencies, 50),
                    'p95' => $percentile($latencies, 95),
                    'p99' => $percentile($latencies, 99),
                    'currentRss' => array_sum($currentRss),
                    'peakWorkerRss' => $peakWorkerRss,
                    'rssSamples' => $rssSamples,
                    'workerCount' => $workerCount,
                ];
            } finally {
                gatewayPlannerStopWorkerman($handle);
            }
        },
    );
} catch (Throwable $e) {
    gatewayPlannerRemoveDataDirectory($dataDirectory);
    fwrite(STDERR, "BENCHMARK FAILED: ".$e->getMessage()."\n");
    exit(1);
}

gatewayPlannerRemoveDataDirectory($dataDirectory);

$throughput = $metrics['totalTime'] > 0 ? $metrics['successful'] / $metrics['totalTime'] : 0.0;

echo "\n";
echo "Workerman Planner benchmark\n";
echo "==========================\n";
echo "Runtime:             PHP ".PHP_VERSION.' / Workerman '.\Workerman\Worker::VERSION."\n";
echo "Workers:             {$metrics['workerCount']}\n";
echo "Workload:            authenticated POST /_internal/gateway/plan (DB-backed relay)\n";
echo "Requests:            {$metrics['successful']} successful / {$metrics['errors']} failed (warmup $warmup)\n";
echo 'Total time:          '.number_format($metrics['totalTime'], 4)." s\n";
echo 'Throughput:          '.number_format($throughput, 2)." req/s\n";
echo 'Latency avg:         '.number_format($metrics['avgLatency'] * 1000, 2)." ms\n";
echo 'Latency p50:         '.number_format($metrics['p50'] * 1000, 2)." ms\n";
echo 'Latency p95:         '.number_format($metrics['p95'] * 1000, 2)." ms\n";
echo 'Latency p99:         '.number_format($metrics['p99'] * 1000, 2)." ms\n";
echo 'Worker RSS current:  '.number_format($metrics['currentRss'] / 1024 / 1024, 2)." MiB aggregate\n";
echo 'Worker RSS peak:     '.number_format($metrics['peakWorkerRss'] / 1024 / 1024, 2)." MiB (single worker max)\n";
echo 'RSS samples:         '.$metrics['rssSamples']."\n";
echo "\n";
echo "Methodology:\n";
echo "  - Worker RSS sampled every $sampleEvery requests via ps(1); 'current' is the\n";
echo "    last aggregate, 'peak' is the max single-worker value observed.\n";
echo "  - Latency is client-side wall-clock including full request handling.\n";
echo "  - Disposable SQLite database, fresh schema, one seeded relay endpoint.\n";
echo "\n";
echo "Limitations:\n";
echo "  - No Octane server (frankenphp / swoole / roadrunner) head-to-head was run;\n";
echo "    the same-workload Octane comparison is OUTSTANDING.\n";
echo "  - Sequential single-client requests; production concurrency and\n";
echo "    PostgreSQL/MySQL behavior will differ.\n";

exit($metrics['errors'] === 0 && $metrics['successful'] > 0 ? 0 : 1);
