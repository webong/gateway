#!/usr/bin/env php
<?php

declare(strict_types=1);

/**
 * Planner host benchmark: one harness, one workload, several real hosts.
 *
 * Every host is a container of the same Planner image (infra/docker/
 * Gateway.planner.Dockerfile, built from this checkout) started through the
 * project's own launcher, `gateway-planner-start serve`, with
 * GATEWAY_PLANNER_SERVER selecting the runtime:
 *
 *   workerman   Workerman (app/workerman.php), shared PHP settings
 *   frankenphp  Laravel Octane on FrankenPHP, shared PHP settings
 *   workerman-shipped, frankenphp-shipped
 *               The same hosts with the image's own PHP settings (no shared ini; for
 *               Workerman that means opcache.enable_cli=0). Reference only: not an
 *               equal-settings comparison.
 *
 * Both hosts get the same worker count, recycling threshold, CPU and memory
 * limit, environment and PHP settings; each serves its own PostgreSQL database,
 * cloned from one migrated and seeded template, on one PostgreSQL server.
 *
 * The workload is an authenticated POST /_internal/gateway/plan to
 * /http-smoke/registry-ingress. Every measured response must be HTTP 200 and the
 * seeded relay plan (action, relay URL, subscriber, endpoint key); anything else
 * (timeout, transport error, HTTP error, wrong plan) is counted as a failure.
 * The client keeps connections alive and uses the same connection behaviour for
 * every host.
 *
 * Memory is sampled by a separate process outside the timed client loop.
 *
 * Usage:
 *   php scripts/bench-planner-hosts.php --build-only            build the image from this tree and exit
 *   php scripts/bench-planner-hosts.php --build [options]       build the image, then run the benchmark
 *   php scripts/bench-planner-hosts.php [options]               run the benchmark (image must match this tree)
 *   php scripts/bench-planner-hosts.php --check-shutdown        container graceful-stop check
 *   php scripts/bench-planner-hosts.php --shutdown-matrix=stop:7,kill:7,stop:0   one case per MODE:HOLD_SECONDS (MODE: stop|kill)
 *
 * Options (defaults in brackets):
 *   --image=NAME [gateway-planner-bench:local]   --hosts=LIST [workerman,frankenphp]
 *   --workers=N [4]            --max-requests=N [500]    --cpus=N [4]   --memory=SIZE [2g]
 *   --concurrency=LIST [1,8]   --requests=N [5000]       --repetitions=N [3]
 *   --warmup=N [1500]          --prewarm=N [200]         --timeout=SECONDS [10]
 *   --sample-interval=SECONDS [0.5]   --out=DIR [temp]   --postgres-image=NAME [postgres:18.2]
 *   --allow-stale-image        run even if the image was built from a different tree
 *   --launcher-file=PATH       (--check-shutdown) bind-mount another gateway-planner-start
 *
 * Nothing outside what this script creates is touched: containers, network and
 * databases are named gw-bench-<id>-* and removed on exit.
 */

const BENCH_DOCKERFILE = 'infra/docker/Gateway.planner.Dockerfile';
const BENCH_EXPECTED_RELAY_URL = 'https://subscriber.example.test/hook';
const BENCH_EXPECTED_SUBSCRIBER = 'workspace-42';
const BENCH_EXPECTED_ENDPOINT = 'http-smoke-endpoint';
const BENCH_INTERNAL_TOKEN = 'bench-internal-token';
const BENCH_REGISTRY_TOKEN = 'bench-registry-token';

exit(benchMain($argv));

// ---------------------------------------------------------------------------
// Entry
// ---------------------------------------------------------------------------

function benchMain(array $argv): int
{
    $options = getopt('', [
        'image::', 'hosts::', 'workers::', 'max-requests::', 'cpus::', 'memory::', 'concurrency::',
        'requests::', 'repetitions::', 'warmup::', 'prewarm::', 'timeout::', 'sample-interval::',
        'out::', 'postgres-image::', 'build', 'build-only', 'allow-stale-image', 'check-shutdown', 'diagnose-shutdown', 'shutdown-matrix::', 'strace', 'launcher-file::',
        'sample', 'container::', 'sample-out::', 'help',
    ]);

    if (isset($options['help'])) {
        echo "See the header of scripts/bench-planner-hosts.php for usage.\n";

        return 0;
    }
    if (isset($options['sample'])) {
        return benchSamplerMain((string) $options['container'], (string) $options['sample-out'], (float) ($options['sample-interval'] ?? 0.5));
    }

    $config = [
        'image' => (string) ($options['image'] ?? 'gateway-planner-bench:local'),
        'hosts' => array_values(array_filter(explode(',', (string) ($options['hosts'] ?? 'workerman,frankenphp')))),
        'workers' => (int) ($options['workers'] ?? 4),
        'maxRequests' => (int) ($options['max-requests'] ?? 500),
        'cpus' => (string) ($options['cpus'] ?? '4'),
        'memory' => (string) ($options['memory'] ?? '2g'),
        'concurrency' => array_map('intval', explode(',', (string) ($options['concurrency'] ?? '1,8'))),
        'requests' => (int) ($options['requests'] ?? 5000),
        'repetitions' => (int) ($options['repetitions'] ?? 3),
        'warmup' => (int) ($options['warmup'] ?? 1500),
        'prewarm' => (int) ($options['prewarm'] ?? 200),
        'timeout' => (float) ($options['timeout'] ?? 10),
        'sampleInterval' => (float) ($options['sample-interval'] ?? 0.5),
        'out' => (string) ($options['out'] ?? (sys_get_temp_dir().'/gateway-planner-bench-'.date('Ymd-His'))),
        'postgresImage' => (string) ($options['postgres-image'] ?? 'postgres:18.2'),
        'allowStale' => isset($options['allow-stale-image']),
        'strace' => isset($options['strace']),
        'launcherFile' => isset($options['launcher-file']) ? (string) $options['launcher-file'] : null,
    ];
    @mkdir($config['out'], 0755, true);

    $root = dirname(__DIR__);
    $GLOBALS['bench'] = ['root' => $root, 'commands' => [], 'containers' => [], 'networks' => [], 'id' => bin2hex(random_bytes(3))];
    benchInstallCleanup();

    try {
        if (isset($options['build']) || isset($options['build-only'])) {
            benchBuildImage($config);
            if (isset($options['build-only'])) {
                return 0;
            }
        }
        benchRequireImage($config);

        if (isset($options['shutdown-matrix'])) {
            return benchShutdownMatrix($config, (string) $options['shutdown-matrix']);
        }
        if (isset($options['check-shutdown']) || isset($options['diagnose-shutdown'])) {
            return benchCheckShutdown($config, isset($options['diagnose-shutdown']));
        }

        return benchRun($config);
    } catch (Throwable $e) {
        fwrite(STDERR, "\nBENCHMARK FAILED: ".$e->getMessage()."\n");

        return 1;
    } finally {
        file_put_contents($config['out'].'/commands.txt', implode("\n", $GLOBALS['bench']['commands'])."\n");
        benchCleanup();
    }
}

// ---------------------------------------------------------------------------
// Process + docker helpers
// ---------------------------------------------------------------------------

/**
 * Run a command without a shell. Returns stdout/stderr/exit code; throws on a
 * non-zero exit unless $check is false.
 *
 * @param list<string> $command
 * @return array{code: int, out: string, err: string}
 */
function benchExec(array $command, bool $check = true, bool $record = true): array
{
    if ($record) {
        $GLOBALS['bench']['commands'][] = implode(' ', array_map('escapeshellarg', $command));
    }
    $process = proc_open($command, [1 => ['pipe', 'w'], 2 => ['pipe', 'w']], $pipes);
    if (! is_resource($process)) {
        throw new RuntimeException('Unable to run: '.implode(' ', $command));
    }
    $out = stream_get_contents($pipes[1]);
    $err = stream_get_contents($pipes[2]);
    fclose($pipes[1]);
    fclose($pipes[2]);
    $code = proc_close($process);
    if ($check && $code !== 0) {
        throw new RuntimeException('Command failed ('.$code."): ".implode(' ', $command)."\n".trim($err ?: $out));
    }

    return ['code' => $code, 'out' => (string) $out, 'err' => (string) $err];
}

function benchDocker(string ...$arguments): array
{
    return benchExec(['docker', ...$arguments]);
}

function benchContainerName(string $suffix): string
{
    return 'gw-bench-'.$GLOBALS['bench']['id'].'-'.$suffix;
}

function benchInstallCleanup(): void
{
    register_shutdown_function('benchCleanup');
    if (function_exists('pcntl_signal')) {
        pcntl_async_signals(true);
        foreach ([SIGINT, SIGTERM] as $signal) {
            pcntl_signal($signal, static function (): void {
                benchCleanup();
                exit(130);
            });
        }
    }
}

/** Remove only the containers and network this run created. */
function benchCleanup(): void
{
    static $done = false;
    if ($done || ! isset($GLOBALS['bench'])) {
        return;
    }
    $done = true;
    foreach (array_reverse($GLOBALS['bench']['containers']) as $name) {
        benchExec(['docker', 'rm', '-f', $name], false, false);
    }
    foreach ($GLOBALS['bench']['networks'] as $name) {
        benchExec(['docker', 'network', 'rm', $name], false, false);
    }
}

/** Fingerprint of everything that goes into the image (HEAD + uncommitted + untracked), excluding the harness itself. */
function benchTreeFingerprint(): array
{
    $root = $GLOBALS['bench']['root'];
    $exclude = ['--', '.', ':(exclude)scripts/bench-planner-hosts.php', ':(exclude)scripts/bench'];
    $git = static fn (array $arguments): string => benchExec(['git', '-C', $root, ...$arguments], true, false)['out'];

    $head = trim($git(['rev-parse', 'HEAD']));
    $status = trim($git(['status', '--short', ...$exclude]));
    $diff = $git(['diff', 'HEAD', ...$exclude]);
    $untracked = array_filter(explode("\n", trim($git(['ls-files', '--others', '--exclude-standard', ...$exclude]))));
    $hashes = [];
    foreach ($untracked as $file) {
        $hashes[] = $file.' '.sha1_file($root.'/'.$file);
    }

    return [
        'head' => $head,
        'subject' => trim($git(['log', '-1', '--format=%s'])),
        'status' => $status,
        'fingerprint' => sha1($head."\n".$diff."\n".implode("\n", $hashes)),
    ];
}

function benchBuildImage(array $config): void
{
    $tree = benchTreeFingerprint();
    echo "Building {$config['image']} from {$tree['head']} (tree {$tree['fingerprint']})...\n";
    $command = [
        'docker', 'build', '-f', BENCH_DOCKERFILE,
        '--label', 'gateway.bench.tree='.$tree['fingerprint'],
        '--label', 'gateway.bench.head='.$tree['head'],
        '-t', $config['image'], '.',
    ];
    $GLOBALS['bench']['commands'][] = '(cd '.escapeshellarg($GLOBALS['bench']['root']).' && '.implode(' ', array_map('escapeshellarg', $command)).')';
    $process = proc_open($command, [1 => ['file', $config['out'].'/image-build.log', 'w'], 2 => ['file', $config['out'].'/image-build.log', 'a']], $pipes, $GLOBALS['bench']['root']);
    $code = is_resource($process) ? proc_close($process) : 1;
    if ($code !== 0) {
        throw new RuntimeException("docker build failed ($code); see {$config['out']}/image-build.log");
    }
    echo "Image built.\n";
}

function benchRequireImage(array $config): void
{
    $inspect = benchExec(['docker', 'image', 'inspect', $config['image'], '--format', '{{.Id}}|{{index .Config.Labels "gateway.bench.tree"}}'], false, false);
    if ($inspect['code'] !== 0) {
        throw new RuntimeException("Image {$config['image']} not found. Build it with: php scripts/bench-planner-hosts.php --build");
    }
    [$id, $label] = array_pad(explode('|', trim($inspect['out'])), 2, '');
    $tree = benchTreeFingerprint();
    $GLOBALS['bench']['image'] = ['id' => $id, 'tree' => $label, 'current' => $tree];
    if ($label !== $tree['fingerprint'] && ! $config['allowStale']) {
        throw new RuntimeException(
            "Image {$config['image']} was built from tree [".($label ?: 'unlabelled')."], but the working tree is [{$tree['fingerprint']}]. "
            .'Rebuild with --build (or pass --allow-stale-image).',
        );
    }
}

// ---------------------------------------------------------------------------
// Infrastructure: PostgreSQL, template database, host containers
// ---------------------------------------------------------------------------

/** @return array{network: string, postgres: string, appKey: string, identity: array<string, mixed>} */
function benchInfrastructure(array $config): array
{
    $network = benchContainerName('net');
    benchDocker('network', 'create', $network);
    $GLOBALS['bench']['networks'][] = $network;

    $postgres = benchContainerName('pg');
    benchDocker(
        'run', '-d', '--name', $postgres, '--network', $network,
        '-e', 'POSTGRES_PASSWORD=bench', $config['postgresImage'],
        '-c', 'max_connections=300',
    );
    $GLOBALS['bench']['containers'][] = $postgres;
    $deadline = microtime(true) + 90;
    while (benchExec(['docker', 'exec', $postgres, 'pg_isready', '-U', 'postgres', '-h', '127.0.0.1'], false, false)['code'] !== 0) {
        if (microtime(true) > $deadline) {
            throw new RuntimeException('PostgreSQL did not become ready.');
        }
        usleep(500_000);
    }
    // The official image restarts once after initialisation; wait for it to settle.
    sleep(2);

    $infra = ['network' => $network, 'postgres' => $postgres, 'appKey' => 'base64:'.base64_encode(random_bytes(32))];

    // One migrated, seeded template; every host gets a clone of it.
    benchPsql($postgres, 'postgres', 'CREATE DATABASE bench_template');
    $template = 'bench_template';
    $expected = benchServerIdentity($postgres, $template);
    benchAssertResolved($infra, $config, $template, benchResolveInOneOff($infra, $config, $template), $expected);
    benchOneOff($infra, $config, $template, ['migrate']);
    benchAssertResolved($infra, $config, $template, benchResolveInOneOff($infra, $config, $template), $expected);
    benchOneOff($infra, $config, $template, [], [
        '--entrypoint', 'php',
        '-e', 'GATEWAY_SEED_ENDPOINT_KEY='.BENCH_EXPECTED_ENDPOINT,
        '-e', 'GATEWAY_SEED_ROUTING_SCOPE=application',
        '-e', 'GATEWAY_SEED_ROUTING_KEY=app-123',
        '-e', 'GATEWAY_SEED_SUBSCRIBER_ID='.BENCH_EXPECTED_SUBSCRIBER,
        '-e', 'GATEWAY_SEED_URL='.BENCH_EXPECTED_RELAY_URL,
    ], ['/gateway/tests/Support/seed-planner-endpoint.php']);

    $counts = trim(benchPsql($postgres, $template, "SELECT (SELECT count(*) FROM endpoints) || '|' || (SELECT count(*) FROM subscriptions)"));
    if ($counts !== '1|1') {
        throw new RuntimeException("Template seed unexpected (endpoints|subscriptions = $counts).");
    }

    $infra['identity'] = $expected;

    return $infra;
}

function benchPsql(string $postgres, string $database, string $sql): string
{
    return benchExec([
        'docker', 'exec', '-e', 'PGPASSWORD=bench', $postgres,
        'psql', '-U', 'postgres', '-h', '127.0.0.1', '-d', $database, '-tAX', '-v', 'ON_ERROR_STOP=1', '-c', $sql,
    ])['out'];
}

/** @return array{database: string, port: int, started: string} the server a client reaches, seen from the server side */
function benchServerIdentity(string $postgres, string $database): array
{
    $row = explode('|', trim(benchPsql($postgres, $database, "SELECT current_database() || '|' || inet_server_port() || '|' || pg_postmaster_start_time()::text")));

    return ['database' => $row[0], 'port' => (int) $row[1], 'started' => $row[2]];
}

/** @return array<string, string> */
function benchHostEnvironment(array $infra, array $config, string $database): array
{
    return [
        'APP_KEY' => $infra['appKey'],
        'GATEWAY_INTERNAL_TOKEN' => BENCH_INTERNAL_TOKEN,
        'REGISTRY_TOKEN' => BENCH_REGISTRY_TOKEN,
        'DB_CONNECTION' => 'pgsql',
        'DB_URL' => 'postgresql://postgres:bench@'.$infra['postgres'].':5432/'.$database,
        'DB_DATABASE' => $database,
        'GATEWAY_PATH_RESOLVER' => 'Gateway\\Router\\BenchHttpSmokePathResolver',
        'GATEWAY_PLANNER_WORKERS' => (string) $config['workers'],
        'GATEWAY_PLANNER_MAX_REQUESTS' => (string) $config['maxRequests'],
    ];
}

/** @return list<string> */
function benchEnvironmentFlags(array $environment): array
{
    $flags = [];
    foreach ($environment as $name => $value) {
        array_push($flags, '-e', "$name=$value");
    }

    return $flags;
}

function benchResolverMount(): array
{
    return ['-v', $GLOBALS['bench']['root'].'/scripts/bench/BenchHttpSmokePathResolver.php:/gateway/app/BenchHttpSmokePathResolver.php:ro'];
}

/**
 * Run a one-off container of the image on the benchmark network.
 *
 * @param list<string> $arguments container command arguments
 * @param list<string> $extraFlags docker run flags
 * @param list<string> $command    overrides the image command when an --entrypoint is given
 */
function benchOneOff(array $infra, array $config, string $database, array $arguments, array $extraFlags = [], array $command = []): array
{
    return benchExec([
        'docker', 'run', '--rm', '--network', $infra['network'],
        ...benchEnvironmentFlags(benchHostEnvironment($infra, $config, $database)),
        ...benchResolverMount(), ...$extraFlags,
        $config['image'], ...($command ?: $arguments),
    ]);
}

/** @return array<string, mixed> what Laravel resolves in a fresh container (reads only) */
function benchResolveInOneOff(array $infra, array $config, string $database): array
{
    $result = benchOneOff($infra, $config, $database, [], ['--entrypoint', 'php'], ['/gateway/tests/Support/resolve-planner-database.php']);

    return benchParseReport($result['out']);
}

/** @return array<string, mixed> */
function benchParseReport(string $output): array
{
    foreach (array_reverse(explode("\n", trim($output))) as $line) {
        if (str_starts_with(ltrim($line), '{')) {
            return json_decode($line, true, 512, JSON_THROW_ON_ERROR);
        }
    }
    throw new RuntimeException("No resolved-database report in output:\n$output");
}

/**
 * Refuse to proceed unless Laravel resolves exactly the intended database on the
 * intended server (the same server process the template was created on).
 */
function benchAssertResolved(array $infra, array $config, string $expectedDatabase, array $report, array $expectedIdentity): void
{
    $problems = [];
    if (($report['driver'] ?? null) !== 'pgsql') {
        $problems[] = 'driver '.json_encode($report['driver'] ?? null);
    }
    if (($report['database'] ?? null) !== $expectedDatabase) {
        $problems[] = 'database '.json_encode($report['database'] ?? null)." (expected $expectedDatabase)";
    }
    if (($report['host'] ?? null) !== $infra['postgres'] || ($report['port'] ?? null) !== 5432) {
        $problems[] = 'endpoint '.($report['host'] ?? '?').':'.($report['port'] ?? '?');
    }
    if (($report['config_cached'] ?? true) !== false) {
        $problems[] = 'configuration comes from a cache file';
    }
    $identity = $report['identity'] ?? null;
    if ($identity !== array_merge($expectedIdentity, ['database' => $expectedDatabase]) && $identity !== $expectedIdentity) {
        $problems[] = 'server identity '.json_encode($identity).' (expected '.json_encode($expectedIdentity).')';
    }
    if ($problems !== []) {
        throw new RuntimeException('Refusing to write: Laravel does not resolve the intended database ['.$expectedDatabase.']: '.implode('; ', $problems));
    }
}

/**
 * @return array{name: string, port: int, variant: string, server: string, database: string}
 */
function benchStartHost(array $infra, array $config, string $variant, array $extraFlags = [], string $tag = ''): array
{
    [$server, $sharedIni] = match ($variant) {
        'workerman' => ['workerman', true],
        'workerman-shipped' => ['workerman', false],
        'frankenphp' => ['frankenphp', true],
        'frankenphp-shipped' => ['frankenphp', false],
        'swoole' => ['swoole', true],
        'roadrunner' => ['roadrunner', true],
        default => throw new RuntimeException("Unknown host variant [$variant]."),
    };

    $database = 'bench_'.str_replace('-', '_', $variant).($tag === '' ? '' : '_'.$tag);
    benchPsql($infra['postgres'], 'postgres', "CREATE DATABASE $database TEMPLATE bench_template");
    benchAssertResolved($infra, $config, $database, benchResolveInOneOff($infra, $config, $database), benchServerIdentity($infra['postgres'], $database));

    $name = benchContainerName($variant.($tag === '' ? '' : '-'.$tag));
    $mounts = benchResolverMount();
    if ($sharedIni) {
        $ini = $GLOBALS['bench']['root'].'/scripts/bench/php-bench.ini';
        $mounts = [...$mounts, '-v', "$ini:/usr/local/etc/php/conf.d/zz-bench.ini:ro", '-v', "$ini:/opt/frankenphp/conf.d/zz-bench.ini:ro"];
    }
    $arguments = [
        'run', '-d', '--name', $name, '--network', $infra['network'],
        '--cpus', $config['cpus'], '--memory', $config['memory'], '--memory-swap', $config['memory'],
        '-p', '127.0.0.1::8080',
        ...benchEnvironmentFlags(benchHostEnvironment($infra, $config, $database) + ['GATEWAY_PLANNER_SERVER' => $server]),
        ...$mounts, ...$extraFlags,
        $config['image'], 'serve',
    ];
    $GLOBALS['bench']['containers'][] = $name;
    benchDocker(...$arguments);

    $mapping = trim(benchDocker('port', $name, '8080/tcp')['out']);
    if (! preg_match('/:(\d+)\s*$/m', $mapping, $match)) {
        throw new RuntimeException("Unable to find the published port for $name: $mapping");
    }
    $port = (int) $match[1];

    $deadline = microtime(true) + 180;
    while (true) {
        $state = trim(benchExec(['docker', 'inspect', '-f', '{{.State.Status}}', $name], false, false)['out']);
        if ($state !== 'running') {
            throw new RuntimeException("Host $variant exited during startup:\n".benchStartupLog($name));
        }
        $socket = @stream_socket_client("tcp://127.0.0.1:$port", $errno, $errstr, 1.0);
        if ($socket !== false) {
            fclose($socket);
            $health = benchHttp('GET', "http://127.0.0.1:$port/health", [], '', 2.0);
            if ($health['status'] === 204) {
                break;
            }
        }
        if (microtime(true) > $deadline) {
            throw new RuntimeException("Host $variant did not become healthy:\n".benchStartupLog($name));
        }
        usleep(500_000);
    }

    // The database the *running host* resolves, verified before it serves anything.
    $exec = benchExec(['docker', 'exec', $name, 'php', '/gateway/tests/Support/resolve-planner-database.php']);
    benchAssertResolved($infra, $config, $database, benchParseReport($exec['out']), benchServerIdentity($infra['postgres'], $database));

    return ['name' => $name, 'port' => $port, 'variant' => $variant, 'server' => $server, 'database' => $database];
}

/** The first meaningful container log lines (repeated warnings collapsed). */
function benchStartupLog(string $container): string
{
    $logs = benchExec(['docker', 'logs', $container], false, false);
    $lines = array_filter(explode("\n", $logs['out'].$logs['err']), static fn ($line) => trim($line) !== '');
    $unique = [];
    foreach ($lines as $line) {
        $unique[trim($line)] = true;
    }

    return implode("\n", array_slice(array_keys($unique), 0, 40));
}

// ---------------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------------

/** @return array{status: int, body: string} */
function benchHttp(string $method, string $url, array $headers, string $body, float $timeout): array
{
    $ch = curl_init($url);
    curl_setopt_array($ch, [
        CURLOPT_CUSTOMREQUEST => $method, CURLOPT_RETURNTRANSFER => true, CURLOPT_TIMEOUT_MS => (int) ($timeout * 1000),
        CURLOPT_HTTPHEADER => $headers, CURLOPT_FORBID_REUSE => true,
    ]);
    if ($body !== '') {
        curl_setopt($ch, CURLOPT_POSTFIELDS, $body);
    }
    $response = curl_exec($ch);
    $status = (int) curl_getinfo($ch, CURLINFO_RESPONSE_CODE);

    return ['status' => $status, 'body' => is_string($response) ? $response : ''];
}

function benchPlanBody(): string
{
    return json_encode([
        'delivery_id' => 'dlr-bench',
        'method' => 'POST',
        'path' => '/http-smoke/registry-ingress',
        'body' => '',
        'headers' => [],
    ], JSON_THROW_ON_ERROR);
}

/** @return string|null a description of what is wrong with the plan, or null when it is the expected relay plan */
function benchPlanProblem(string $body): ?string
{
    $plan = json_decode($body, true);
    if (! is_array($plan)) {
        return 'not JSON';
    }
    if (($plan['action'] ?? null) !== 'relay') {
        return 'action '.json_encode($plan['action'] ?? null);
    }
    $relay = $plan['relays'][0] ?? [];
    if (($relay['url'] ?? null) !== BENCH_EXPECTED_RELAY_URL || ($relay['subscriber_id'] ?? null) !== BENCH_EXPECTED_SUBSCRIBER) {
        return 'relay '.json_encode($relay);
    }
    if (($plan['metadata']['endpoint_key'] ?? null) !== BENCH_EXPECTED_ENDPOINT) {
        return 'endpoint '.json_encode($plan['metadata']['endpoint_key'] ?? null);
    }

    return null;
}

/**
 * Drive the workload with $concurrency clients, each keeping its connection
 * alive, until $total requests have been issued. Every response is validated.
 *
 * @return array{requests: int, ok: int, failures: array<string, int>, latenciesMs: list<float>, wallSeconds: float, sampleProblems: list<string>}
 */
function benchLoad(int $port, int $concurrency, int $total, float $timeout): array
{
    $url = "http://127.0.0.1:$port/_internal/gateway/plan";
    $body = benchPlanBody();
    $headers = ['Content-Type: application/json', 'X-Gateway-Internal: '.BENCH_INTERNAL_TOKEN, 'Expect:'];

    $multi = curl_multi_init();
    $started = 0;
    $finished = 0;
    $result = ['requests' => $total, 'ok' => 0, 'failures' => ['timeout' => 0, 'transport' => 0, 'http_error' => 0, 'wrong_plan' => 0], 'latenciesMs' => [], 'wallSeconds' => 0.0, 'sampleProblems' => []];

    $makeHandle = static function () use ($url, $body, $headers, $timeout) {
        $ch = curl_init($url);
        curl_setopt_array($ch, [
            CURLOPT_POST => true, CURLOPT_POSTFIELDS => $body, CURLOPT_HTTPHEADER => $headers,
            CURLOPT_RETURNTRANSFER => true, CURLOPT_TIMEOUT_MS => (int) ($timeout * 1000), CURLOPT_CONNECTTIMEOUT_MS => (int) ($timeout * 1000),
            CURLOPT_HTTP_VERSION => CURL_HTTP_VERSION_1_1, CURLOPT_TCP_NODELAY => true,
            CURLOPT_FORBID_REUSE => false, CURLOPT_FRESH_CONNECT => false,
        ]);

        return $ch;
    };

    $handles = [];
    for ($i = 0; $i < $concurrency && $started < $total; $i++) {
        $handles[] = $ch = $makeHandle();
        curl_multi_add_handle($multi, $ch);
        $started++;
    }

    $begin = hrtime(true);
    $running = 0;
    do {
        curl_multi_exec($multi, $running);
        while (($info = curl_multi_info_read($multi)) !== false) {
            $ch = $info['handle'];
            $finished++;
            $status = (int) curl_getinfo($ch, CURLINFO_RESPONSE_CODE);
            $content = (string) curl_multi_getcontent($ch);
            $note = null;
            if ($info['result'] === CURLE_OPERATION_TIMEDOUT) {
                $result['failures']['timeout']++;
                $note = 'timeout';
            } elseif ($info['result'] !== CURLE_OK) {
                $result['failures']['transport']++;
                $note = 'transport: '.curl_strerror($info['result']);
            } elseif ($status !== 200) {
                $result['failures']['http_error']++;
                $note = "HTTP $status";
            } elseif (($problem = benchPlanProblem($content)) !== null) {
                $result['failures']['wrong_plan']++;
                $note = "wrong plan: $problem";
            } else {
                $result['ok']++;
                $result['latenciesMs'][] = curl_getinfo($ch, CURLINFO_TOTAL_TIME) * 1000.0;
            }
            if ($note !== null && count($result['sampleProblems']) < 5) {
                $result['sampleProblems'][] = $note;
            }

            curl_multi_remove_handle($multi, $ch);
            if ($started < $total) {
                // Same easy handle, same connection: the client keeps its keep-alive connection.
                curl_multi_add_handle($multi, $ch);
                $started++;
                curl_multi_exec($multi, $running);
            }
        }
        if ($running > 0) {
            curl_multi_select($multi, 0.05);
        }
    } while ($running > 0 || $finished < $total);
    $result['wallSeconds'] = (hrtime(true) - $begin) / 1e9;
    curl_multi_close($multi);

    return $result;
}

function benchPercentile(array $sortedValues, float $percentile): float
{
    if ($sortedValues === []) {
        return NAN;
    }
    $index = (int) ceil(($percentile / 100) * count($sortedValues)) - 1;

    return $sortedValues[max(0, min($index, count($sortedValues) - 1))];
}

function benchMedian(array $values): float
{
    $values = array_values(array_filter($values, static fn ($value) => ! is_nan((float) $value)));
    if ($values === []) {
        return NAN;
    }
    sort($values);
    $middle = intdiv(count($values), 2);

    return count($values) % 2 ? (float) $values[$middle] : ($values[$middle - 1] + $values[$middle]) / 2;
}

// ---------------------------------------------------------------------------
// Memory sampling (separate process, outside the timed client loop)
// ---------------------------------------------------------------------------

/** Internal mode: sample every process in a container until terminated. */
function benchSamplerMain(string $container, string $outFile, float $interval): int
{
    $stop = false;
    pcntl_async_signals(true);
    pcntl_signal(SIGTERM, static function () use (&$stop): void {
        $stop = true;
    });

    $handle = fopen($outFile, 'w');
    while (! $stop) {
        $started = microtime(true);
        $snapshot = benchProcessSnapshot($container);
        if ($snapshot !== null) {
            fwrite($handle, json_encode(['t' => $started] + $snapshot)."\n");
            fflush($handle);
        }
        $sleep = $interval - (microtime(true) - $started);
        if ($sleep > 0) {
            usleep((int) ($sleep * 1_000_000));
        }
    }
    fclose($handle);

    return 0;
}

/**
 * Every process in the container with its resident set size, read from /proc.
 *
 * @return array{processes: list<array{pid: int, ppid: int, name: string, rssKb: int}>, cgroupBytes: int|null}|null
 */
function benchProcessSnapshot(string $container): ?array
{
    $script = <<<'SH'
for f in /proc/[0-9]*/status; do awk '/^Pid:/{p=$2} /^PPid:/{q=$2} /^Name:/{sub(/^Name:[ \t]*/,""); n=$0} /^VmRSS:/{r=$2} END{ if (r != "") printf "%s\t%s\t%s\t%s\n", p, q, n, r }' "$f" 2>/dev/null; done
printf 'cgroup\t%s\n' "$(cat /sys/fs/cgroup/memory.current 2>/dev/null)"
SH;
    $result = benchExec(['docker', 'exec', $container, 'sh', '-c', $script], false, false);
    if ($result['code'] !== 0) {
        return null;
    }
    $processes = [];
    $cgroup = null;
    foreach (explode("\n", trim($result['out'])) as $line) {
        $fields = explode("\t", $line);
        if ($fields[0] === 'cgroup') {
            $cgroup = isset($fields[1]) && $fields[1] !== '' ? (int) $fields[1] : null;
        } elseif (count($fields) === 4) {
            $processes[] = ['pid' => (int) $fields[0], 'ppid' => (int) $fields[1], 'name' => $fields[2], 'rssKb' => (int) $fields[3]];
        }
    }

    return ['processes' => $processes, 'cgroupBytes' => $cgroup];
}

/** @return array{proc: resource, file: string} */
function benchStartSampler(array $config, string $container, string $file)
{
    $process = proc_open(
        [PHP_BINARY, __FILE__, '--sample', '--container='.$container, '--sample-out='.$file, '--sample-interval='.$config['sampleInterval']],
        [0 => ['file', '/dev/null', 'r'], 1 => ['file', '/dev/null', 'w'], 2 => ['file', '/dev/null', 'w']],
        $pipes,
    );
    if (! is_resource($process)) {
        throw new RuntimeException('Unable to start the memory sampler.');
    }

    return ['proc' => $process, 'file' => $file];
}

/**
 * The role of each process for a host variant, from its name and its place in the
 * process tree: "serving" processes handle requests, "master" supervises them,
 * "launcher" is the `php artisan octane:start` wrapper.
 *
 * @param list<array{pid: int, ppid: int, name: string, rssKb: int}> $processes
 * @return array<int, string> role by pid
 */
function benchRoles(string $server, array $processes): array
{
    $roles = [];
    $byName = static function (string $name) use ($processes): array {
        return array_column(array_filter($processes, static fn ($p) => $p['name'] === $name), 'pid');
    };

    foreach ($processes as $process) {
        $name = $process['name'];
        $role = 'other';
        if ($server === 'workerman') {
            // No process titles are set, so go by the tree: the master is the `php` started by the
            // launcher; its `php` children are the workers.
            $parent = array_values(array_filter($processes, static fn ($p) => $p['pid'] === $process['ppid']))[0] ?? null;
            $role = $name !== 'php' ? 'other' : ($parent !== null && $parent['name'] === 'php' ? 'serving' : 'master');
        } elseif ($server === 'frankenphp') {
            // FrankenPHP serves from threads inside one process; the Octane launcher is `php`.
            $role = str_starts_with($name, 'frankenphp') ? 'serving' : ($name === 'php' ? 'launcher' : 'other');
        } elseif ($server === 'roadrunner') {
            // rr supervises PHP workers it spawned; the other `php` is the Octane launcher.
            $role = $name === 'rr' ? 'master' : ($name === 'php' ? (in_array($process['ppid'], $byName('rr'), true) ? 'serving' : 'launcher') : 'other');
        } elseif ($server === 'swoole') {
            // Swoole: workers and task workers are children of the manager, itself a child of the master.
            $parent = array_values(array_filter($processes, static fn ($p) => $p['pid'] === $process['ppid']))[0] ?? null;
            $role = $name !== 'php' ? 'other' : ($parent !== null && $parent['name'] === 'php' && $parent['ppid'] > 1 ? 'serving' : 'launcher');
        }
        $roles[$process['pid']] = $role;
    }

    return $roles;
}

/**
 * @param array{proc: resource, file: string} $sampler
 * @return array{samples: int, servingProcessesMax: int, servingPeakMiB: float, servingMeanMiB: float, masterPeakMiB: float, launcherPeakMiB: float, cgroupPeakMiB: float}
 */
function benchStopSampler(array $sampler, string $server): array
{
    proc_terminate($sampler['proc'], SIGTERM);
    proc_close($sampler['proc']);

    $samples = 0;
    $serving = [];
    $servingCount = 0;
    $master = 0;
    $launcher = 0;
    $cgroup = 0;
    foreach (file($sampler['file'], FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES) ?: [] as $line) {
        $sample = json_decode($line, true);
        if (! is_array($sample)) {
            continue;
        }
        $samples++;
        $sums = ['serving' => 0, 'master' => 0, 'launcher' => 0];
        $count = 0;
        $roles = benchRoles($server, $sample['processes']);
        foreach ($sample['processes'] as $process) {
            $role = $roles[$process['pid']];
            if (isset($sums[$role])) {
                $sums[$role] += $process['rssKb'];
                $count += $role === 'serving' ? 1 : 0;
            }
        }
        $serving[] = $sums['serving'];
        $servingCount = max($servingCount, $count);
        $master = max($master, $sums['master']);
        $launcher = max($launcher, $sums['launcher']);
        $cgroup = max($cgroup, (int) ($sample['cgroupBytes'] ?? 0));
    }

    return [
        'samples' => $samples,
        'servingProcessesMax' => $servingCount,
        'servingPeakMiB' => $serving === [] ? NAN : max($serving) / 1024,
        'servingMeanMiB' => $serving === [] ? NAN : array_sum($serving) / count($serving) / 1024,
        'masterPeakMiB' => $master / 1024,
        'launcherPeakMiB' => $launcher / 1024,
        'cgroupPeakMiB' => $cgroup / 1048576,
    ];
}

// ---------------------------------------------------------------------------
// The benchmark
// ---------------------------------------------------------------------------

function benchRun(array $config): int
{
    foreach ($config['hosts'] as $variant) {
        if (! in_array($variant, ['workerman', 'workerman-shipped', 'frankenphp', 'frankenphp-shipped', 'swoole', 'roadrunner'], true)) {
            throw new RuntimeException("Unknown host [$variant].");
        }
    }

    $loadAtStart = sys_getloadavg() ?: [];
    echo "Preparing PostgreSQL and the seeded template (client machine load average: ".implode(' ', array_map(static fn ($v) => number_format($v, 2), $loadAtStart)).")...\n";
    $infra = benchInfrastructure($config);

    $hosts = [];
    foreach ($config['hosts'] as $variant) {
        echo "Starting $variant...\n";
        $hosts[$variant] = benchStartHost($infra, $config, $variant);
        $hosts[$variant]['versions'] = benchHostVersions($hosts[$variant]);
        $hosts[$variant]['settings'] = benchHostSettings($hosts[$variant]);

        // Initial warm-up: not measured.
        benchLoad($hosts[$variant]['port'], 8, $config['warmup'], $config['timeout']);
        benchLoad($hosts[$variant]['port'], 1, intdiv($config['warmup'], 5), $config['timeout']);

        $snapshot = benchProcessSnapshot($hosts[$variant]['name']) ?? ['processes' => []];
        $hosts[$variant]['processes'] = $snapshot['processes'];
        echo "  processes in $variant after warm-up:\n";
        $roles = benchRoles($hosts[$variant]['server'], $snapshot['processes']);
        foreach ($snapshot['processes'] as $process) {
            printf("    %-8d ppid %-6d %-28s %8.1f MiB  [%s]\n", $process['pid'], $process['ppid'], $process['name'], $process['rssKb'] / 1024, $roles[$process['pid']]);
        }
    }

    $runs = [];
    for ($repetition = 1; $repetition <= $config['repetitions']; $repetition++) {
        // Alternate which host goes first, so neither benefits from running first.
        $order = $repetition % 2 === 1 ? $config['hosts'] : array_reverse($config['hosts']);
        foreach ($config['concurrency'] as $concurrency) {
            foreach ($order as $variant) {
                $host = $hosts[$variant];
                $state = trim(benchExec(['docker', 'inspect', '-f', '{{.State.Status}}', $host['name']], false, false)['out']);
                if ($state !== 'running') {
                    throw new RuntimeException("Host $variant is no longer running ($state).");
                }
                benchLoad($host['port'], $concurrency, $config['prewarm'], $config['timeout']);

                $sampler = benchStartSampler($config, $host['name'], $config['out']."/samples-$variant-c$concurrency-r$repetition.jsonl");
                usleep((int) ($config['sampleInterval'] * 1_000_000 * 1.5)); // first sample taken before load
                $load = benchLoad($host['port'], $concurrency, $config['requests'], $config['timeout']);
                $memory = benchStopSampler($sampler, $host['server']);

                $latencies = $load['latenciesMs'];
                sort($latencies);
                $run = [
                    'host' => $variant, 'concurrency' => $concurrency, 'repetition' => $repetition,
                    'requests' => $load['requests'], 'ok' => $load['ok'], 'failures' => $load['failures'],
                    'errors' => array_sum($load['failures']),
                    'throughput' => $load['wallSeconds'] > 0 ? $load['ok'] / $load['wallSeconds'] : NAN,
                    'p50' => benchPercentile($latencies, 50), 'p95' => benchPercentile($latencies, 95), 'p99' => benchPercentile($latencies, 99),
                    'wallSeconds' => $load['wallSeconds'], 'memory' => $memory, 'problems' => $load['sampleProblems'],
                ];
                $runs[] = $run;
                printf(
                    "  rep %d c=%d %-18s %6.0f req/s  p50 %6.2f p95 %6.2f p99 %6.2f ms  errors %d  serving RSS peak %.1f MiB\n",
                    $repetition, $concurrency, $variant, $run['throughput'], $run['p50'], $run['p95'], $run['p99'], $run['errors'], $memory['servingPeakMiB'],
                );
            }
        }
    }

    $environment = benchEnvironment($infra);
    $environment['loadAverage'] = ['start' => $loadAtStart, 'end' => sys_getloadavg() ?: []];
    $report = ['config' => $config, 'runs' => $runs, 'hosts' => $hosts, 'environment' => $environment, 'image' => $GLOBALS['bench']['image']];
    file_put_contents($config['out'].'/results.json', json_encode($report, JSON_PRETTY_PRINT | JSON_PARTIAL_OUTPUT_ON_ERROR | JSON_INVALID_UTF8_SUBSTITUTE));
    $markdown = benchMarkdown($report);
    file_put_contents($config['out'].'/results.md', $markdown);
    echo "\n".$markdown."\nResults written to {$config['out']}\n";

    $failed = array_sum(array_column($runs, 'errors'));

    return $failed === 0 ? 0 : 1;
}

/** @return array<string, string> */
function benchHostVersions(array $host): array
{
    $php = trim(benchExec(['docker', 'exec', $host['name'], 'php', '-r', 'echo PHP_VERSION;'])['out']);
    $packages = benchExec(['docker', 'exec', $host['name'], 'php', '-r', <<<'PHP'
$installed = json_decode(file_get_contents('/gateway/vendor/composer/installed.json'), true);
$wanted = ['laravel/framework', 'laravel/octane', 'workerman/workerman', 'webong/gateway'];
$out = [];
foreach ($installed['packages'] ?? $installed as $package) {
    if (in_array($package['name'], $wanted, true)) { $out[$package['name']] = $package['version']; }
}
echo json_encode($out);
PHP]);
    $versions = ['php' => $php] + (json_decode($packages['out'], true) ?: []);
    if ($host['server'] === 'frankenphp') {
        $versions['frankenphp'] = trim(benchExec(['docker', 'exec', $host['name'], 'frankenphp', 'version'])['out']);
    }

    return $versions;
}

/**
 * The INI values each runtime actually reads, taken from its own php (the CLI for
 * Workerman; FrankenPHP's embedded php-cli with its own PHPRC/scan dir).
 *
 * @return array<string, string>
 */
function benchHostSettings(array $host): array
{
    $keys = ['memory_limit', 'opcache.enable', 'opcache.enable_cli', 'opcache.memory_consumption', 'opcache.validate_timestamps', 'opcache.jit', 'zend.assertions'];
    $code = 'echo json_encode(array_combine('.var_export($keys, true).', array_map(static fn ($k) => (string) ini_get($k), '.var_export($keys, true).')));';
    $command = $host['server'] === 'frankenphp'
        ? ['docker', 'exec', $host['name'], 'frankenphp', 'php-cli', '-r', $code]
        : ['docker', 'exec', $host['name'], 'php', '-r', $code];

    return json_decode(benchExec($command, false)['out'], true) ?: ['unavailable' => 'could not read settings'];
}

/** @return array<string, mixed> */
function benchEnvironment(array $infra): array
{
    $sysctl = static fn (string $key): string => trim(benchExec(['sysctl', '-n', $key], false, false)['out']);
    $info = benchExec(['docker', 'info', '--format', '{{.OperatingSystem}}|{{.NCPU}}|{{.MemTotal}}|{{.ServerVersion}}|{{.Architecture}}'], false, false)['out'];
    [$engineOs, $ncpu, $mem, $engine, $arch] = array_pad(explode('|', trim($info)), 5, '');

    return [
        'host' => php_uname('s').' '.php_uname('r').' '.php_uname('m'),
        'hostCpu' => $sysctl('machdep.cpu.brand_string'),
        'hostCores' => $sysctl('hw.ncpu'),
        'docker' => ['engine' => $engine, 'runtime' => $engineOs, 'cpus' => $ncpu, 'memoryBytes' => (int) $mem, 'arch' => $arch],
        'clientPhp' => PHP_VERSION.' ('.PHP_OS.')',
        'clientCurl' => curl_version()['version'] ?? '?',
        'postgresServer' => trim(benchPsql($infra['postgres'], 'postgres', 'SHOW server_version')),
        'tree' => $GLOBALS['bench']['image']['current'],
    ];
}

function benchMarkdown(array $report): string
{
    $config = $report['config'];
    $runs = $report['runs'];
    $fmt = static fn (float $value, int $decimals = 1): string => is_nan($value) ? 'n/a' : number_format($value, $decimals, '.', '');
    $out = '';

    $out .= "### Per-run results\n\n";
    $out .= "| Host | Workers | Conc. | Rep | Requests | Errors | req/s | p50 ms | p95 ms | p99 ms | Peak serving RSS MiB |\n|---|---|---|---|---|---|---|---|---|---|---|\n";
    foreach ($runs as $run) {
        $out .= sprintf(
            "| %s | %d | %d | %d | %d | %d | %s | %s | %s | %s | %s |\n",
            $run['host'], $config['workers'], $run['concurrency'], $run['repetition'], $run['requests'], $run['errors'],
            $fmt($run['throughput'], 0), $fmt($run['p50'], 2), $fmt($run['p95'], 2), $fmt($run['p99'], 2), $fmt($run['memory']['servingPeakMiB']),
        );
    }

    $out .= "\n### Median across {$config['repetitions']} runs\n\n";
    $out .= "| Host | Runtime | Workers | Conc. | Requests/run | Errors (total) | req/s | p50 ms | p95 ms | p99 ms | Peak serving RSS MiB |\n|---|---|---|---|---|---|---|---|---|---|---|\n";
    foreach ($config['concurrency'] as $concurrency) {
        foreach ($config['hosts'] as $variant) {
            $group = array_values(array_filter($runs, static fn ($run) => $run['host'] === $variant && $run['concurrency'] === $concurrency));
            $versions = $report['hosts'][$variant]['versions'];
            $server = $report['hosts'][$variant]['server'];
            $runtime = match ($server) {
                'frankenphp' => 'Laravel Octane '.($versions['laravel/octane'] ?? '?').' on '.($versions['frankenphp'] ?? 'FrankenPHP'),
                'roadrunner' => 'Laravel Octane '.($versions['laravel/octane'] ?? '?').' on RoadRunner',
                'swoole' => 'Laravel Octane '.($versions['laravel/octane'] ?? '?').' on Swoole',
                default => 'Workerman '.($versions['workerman/workerman'] ?? '?'),
            };
            $column = static fn (callable $pick): float => benchMedian(array_map($pick, $group));
            $out .= sprintf(
                "| %s | %s, PHP %s | %d | %d | %d | %d | %s | %s | %s | %s | %s |\n",
                $variant, $runtime, $versions['php'] ?? '?', $config['workers'], $concurrency, $config['requests'], array_sum(array_column($group, 'errors')),
                $fmt($column(static fn ($r) => $r['throughput']), 0), $fmt($column(static fn ($r) => $r['p50']), 2),
                $fmt($column(static fn ($r) => $r['p95']), 2), $fmt($column(static fn ($r) => $r['p99']), 2),
                $fmt($column(static fn ($r) => $r['memory']['servingPeakMiB'])),
            );
        }
    }

    $out .= "\n### Memory methodology\n\n";
    $out .= "- A separate process runs `docker exec <container>` every {$config['sampleInterval']} s and reads `VmRSS` of every process in the container; it runs outside the timed client loop.\n";
    $out .= "- \"Serving\" RSS is the sum over the processes that handle requests: Workerman's worker processes (the master is reported separately), or FrankenPHP's single process (its worker threads share one address space, so this is one number for all threads, not per worker).\n";
    $out .= "- \"Peak\" is the maximum of those sums over the samples taken during the timed run, so it is a sampled peak (it can miss a spike shorter than the interval). Replacement workers created by recycling are included because the sum is taken over whatever is running at each sample.\n";
    $out .= "\n### Per-run memory detail\n\n| Host | Conc. | Rep | Samples | Max serving procs | Serving peak MiB | Serving mean MiB | Master peak MiB | Octane launcher peak MiB | Container cgroup peak MiB |\n|---|---|---|---|---|---|---|---|---|---|\n";
    foreach ($runs as $run) {
        $m = $run['memory'];
        $out .= sprintf(
            "| %s | %d | %d | %d | %d | %s | %s | %s | %s | %s |\n",
            $run['host'], $run['concurrency'], $run['repetition'], $m['samples'], $m['servingProcessesMax'],
            $fmt($m['servingPeakMiB']), $fmt($m['servingMeanMiB']), $fmt($m['masterPeakMiB']), $fmt($m['launcherPeakMiB']), $fmt($m['cgroupPeakMiB']),
        );
    }

    $out .= "\n### Runtime settings actually read (each runtime's own php)\n\n";
    foreach ($config['hosts'] as $variant) {
        $out .= "- **$variant**: ".json_encode($report['hosts'][$variant]['settings'])."\n";
    }

    $env = $report['environment'];
    $out .= "\n### Environment\n\n";
    $out .= "- Tree tested: `{$env['tree']['head']}` ({$env['tree']['subject']}); working-tree fingerprint `{$env['tree']['fingerprint']}`; image `{$report['image']['id']}` built from tree `{$report['image']['tree']}`.\n";
    $out .= "- Uncommitted changes in the tested tree (excluding the harness): ".($env['tree']['status'] === '' ? 'none' : "\n```\n{$env['tree']['status']}\n```")."\n";
    $out .= '- Load average on the client machine (1/5/15 min): at start '.implode(' ', array_map(static fn ($v) => number_format($v, 2), $env['loadAverage']['start'] ?? [])).', at end '.implode(' ', array_map(static fn ($v) => number_format($v, 2), $env['loadAverage']['end'] ?? [])).". Other applications were running on this machine during the measurement.\n";
    $out .= "- Client: macOS host `{$env['host']}`, {$env['hostCpu']} ({$env['hostCores']} cores), PHP {$env['clientPhp']}, curl {$env['clientCurl']}, outside the containers.\n";
    $out .= "- Containers: Docker {$env['docker']['engine']} ({$env['docker']['runtime']}, {$env['docker']['arch']}), VM with {$env['docker']['cpus']} CPUs and ".round($env['docker']['memoryBytes'] / 1073741824, 1)." GiB; each host limited to --cpus {$config['cpus']}, --memory {$config['memory']}; PostgreSQL {$env['postgresServer']} in a container on the same network (default settings except max_connections=300).\n";
    $out .= "- Workload: authenticated POST /_internal/gateway/plan, /http-smoke/registry-ingress, one seeded endpoint and relay subscription; database-backed (PostgreSQL), cache store `database` (the image default for both hosts).\n";
    $out .= "- Warm-up: {$config['warmup']} requests at concurrency 8 plus ".intdiv($config['warmup'], 5)." at 1 per host after start, and {$config['prewarm']} unmeasured requests before every measured run. Recycling after {$config['maxRequests']} requests per worker applies to every host during the measured runs.\n";
    $out .= "- Measured load: {$config['requests']} requests per run at each concurrency, {$config['repetitions']} repetitions, host order alternated per repetition, client connections kept alive, per-request timeout {$config['timeout']} s. Failures (timeout, transport error, non-200, wrong plan) are counted; none are retried.\n";

    return $out;
}

// ---------------------------------------------------------------------------
// Container graceful-stop check
// ---------------------------------------------------------------------------

/**
 * Start Workerman in a container, optionally hold a request inside it, stop the
 * container the given way, and record what happened, second by second.
 *
 * @param string $mode  stop = `docker stop -t N` (the stop signal, then SIGKILL after N seconds);
 *                      kill = `docker kill -s TERM` (the same signal, nothing else)
 * @param float  $hold  seconds the request is held inside the handler (0 = idle container)
 * @return array<string, mixed>
 */
function benchShutdownCase(array $infra, array $config, string $mode, float $hold, string $tag, array $extraFlags = [], int $timeout = 20): array
{
    $holdDirectory = sys_get_temp_dir().'/gateway-bench-hold-'.bin2hex(random_bytes(4));
    mkdir($holdDirectory, 0777, true);
    chmod($holdDirectory, 0777);
    $entered = $holdDirectory.'/entered';

    $extra = ['-v', "$holdDirectory:/bench-hold", '-e', 'GATEWAY_BENCH_HOLD_ENTERED_FILE=/bench-hold/entered', '-e', 'GATEWAY_BENCH_HOLD_SECONDS='.$hold, ...$extraFlags];
    if ($config['launcherFile'] !== null) {
        array_push($extra, '-v', realpath($config['launcherFile']).':/usr/local/bin/gateway-planner-start:ro');
    }
    if ($config['strace']) {
        // Debugging only (needs an image with strace, e.g. --image=gateway-planner-bench:strace).
        array_push($extra, '--cap-add', 'SYS_PTRACE');
    }

    try {
        $host = benchStartHost($infra, $config, 'workerman', $extra, $tag);
        $name = $host['name'];

        // /health answers as soon as ONE worker is up: wait for all of them, so the stop is not
        // sent while some are still booting.
        $deadline = microtime(true) + 60;
        do {
            $snapshot = benchProcessSnapshot($name) ?? ['processes' => []];
            $workers = count(array_filter(benchRoles('workerman', $snapshot['processes']), static fn ($role) => $role === 'serving'));
            usleep(250_000);
        } while ($workers < $config['workers'] && microtime(true) < $deadline);
        sleep(1);

        $socket = null;
        if ($hold > 0) {
            $socket = stream_socket_client("tcp://127.0.0.1:{$host['port']}", $errno, $errstr, 5.0);
            $body = json_encode(['delivery_id' => 'dlr-hold', 'method' => 'POST', 'path' => '/http-smoke/hold', 'body' => '', 'headers' => []], JSON_THROW_ON_ERROR);
            fwrite($socket, "POST /_internal/gateway/plan HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\nContent-Type: application/json\r\nX-Gateway-Internal: ".BENCH_INTERNAL_TOKEN."\r\nContent-Length: ".strlen($body)."\r\n\r\n".$body);
            $until = microtime(true) + 20;
            while (! is_file($entered) && microtime(true) < $until) {
                usleep(20_000);
            }
            if (! is_file($entered)) {
                throw new RuntimeException('The held request never entered the handler.');
            }
            stream_set_blocking($socket, false);
        }

        if ($config['strace']) {
            $traced = array_keys(array_filter(benchRoles('workerman', (benchProcessSnapshot($name) ?? ['processes' => []])['processes']), static fn ($role) => in_array($role, ['serving', 'master'], true)));
            benchExec(['docker', 'exec', '-d', '--user', 'root', $name, 'sh', '-c', 'strace -f -tt -p '.implode(' -p ', $traced).' -o /bench-hold/strace.txt']);
            sleep(2);
        }

        $command = $mode === 'kill' ? ['docker', 'kill', '-s', 'TERM', $name] : ['docker', 'stop', '-t', (string) $timeout, $name];
        $signalled = microtime(true);
        $signal = proc_open($command, [1 => ['pipe', 'w'], 2 => ['pipe', 'w']], $pipes);
        $GLOBALS['bench']['commands'][] = implode(' ', $command);

        // Poll: read the response when it arrives (one response by Content-Length, then close),
        // and note when the container stops being "running".
        $raw = '';
        $length = null;
        $responseAt = null;
        $exitedAt = null;
        $transitions = [];
        $last = null;
        $stopAfter = microtime(true) + $timeout + 8;
        while (microtime(true) < $stopAfter) {
            if ($socket !== null && $responseAt === null) {
                $chunk = @fread($socket, 8192);
                if (is_string($chunk) && $chunk !== '') {
                    $raw .= $chunk;
                }
                $split = strpos($raw, "\r\n\r\n");
                if ($split !== false) {
                    $length ??= preg_match('/^content-length:\s*(\d+)/im', substr($raw, 0, $split), $m) === 1 ? (int) $m[1] : 0;
                    if (strlen($raw) - $split - 4 >= $length) {
                        $responseAt = microtime(true) - $signalled;
                        fclose($socket);
                        $socket = null;
                    }
                } elseif (feof($socket)) {
                    $responseAt = null;
                    fclose($socket);
                    $socket = null;
                    $transitions[] = sprintf('+%.1fs client connection closed without a response', microtime(true) - $signalled);
                }
            }
            $state = trim(benchExec(['docker', 'inspect', '-f', '{{.State.Status}}/{{.State.Running}}', $name], false, false)['out']);
            if ($state !== $last) {
                $transitions[] = sprintf('+%.1fs inspect=%s', microtime(true) - $signalled, $state);
                $last = $state;
            }
            if (str_ends_with($state, '/false')) {
                $exitedAt = microtime(true) - $signalled;
                break;
            }
            usleep(250_000);
        }
        foreach ($pipes as $pipe) {
            fclose($pipe);
        }
        proc_close($signal);

        $state = json_decode(trim(benchExec(['docker', 'inspect', '-f', '{{json .State}}', $name], false, false)['out']), true) ?: [];
        $finished = isset($state['FinishedAt']) ? (new DateTimeImmutable($state['FinishedAt']))->format('U.u') - $signalled : null;
        $log = [];
        $copied = benchExec(['docker', 'cp', $name.':/data/planner/storage/logs/workerman.log', $config['out']."/workerman-$tag.log"], false, false);
        if ($copied['code'] === 0) {
            foreach (file($config['out']."/workerman-$tag.log", FILE_IGNORE_NEW_LINES) ?: [] as $line) {
                if (preg_match('/(received signal|stopping|remains|has been stopped|exit|stop)/i', $line)) {
                    $log[] = preg_replace('/^\S+ \S+ /', '', $line);
                }
            }
        }
        if ($socket !== null) {
            fclose($socket);
        }
        if ($config['strace'] && is_file($holdDirectory.'/strace.txt')) {
            copy($holdDirectory.'/strace.txt', $config['out']."/strace-$tag.txt");
            @unlink($holdDirectory.'/strace.txt');
        }

        return [
            'mode' => $mode, 'hold' => $hold, 'timeout' => $timeout,
            'responseAt' => $responseAt, 'completed' => $responseAt !== null && str_starts_with($raw, 'HTTP/1.1 200') && benchPlanProblem((string) substr($raw, (int) strpos($raw, "\r\n\r\n") + 4)) === null,
            'exitedAt' => $exitedAt, 'exitCode' => (int) ($state['ExitCode'] ?? -1), 'finishedAt' => $finished,
            'transitions' => $transitions, 'log' => $log,
        ];
    } finally {
        @unlink($entered);
        @rmdir($holdDirectory);
    }
}

function benchShutdownCaseLine(array $case): string
{
    return sprintf(
        "%-5s hold %4.1fs | response %s | container exit %s, code %d | killed-at(FinishedAt) %s",
        $case['mode'], $case['hold'],
        $case['hold'] <= 0 ? 'n/a (idle)' : ($case['completed'] ? sprintf('complete at +%.1fs', $case['responseAt']) : 'NOT completed'),
        $case['exitedAt'] === null ? 'NEVER within the window' : sprintf('at +%.1fs', $case['exitedAt']),
        $case['exitCode'],
        $case['finishedAt'] === null ? '?' : sprintf('+%.1fs', $case['finishedAt']),
    );
}

/** Run several cases, one fresh container each, against one shared PostgreSQL and template. */
function benchShutdownMatrix(array $config, string $spec): int
{
    echo "Preparing PostgreSQL and the seeded template...\n";
    $infra = benchInfrastructure($config);

    $cases = [];
    foreach (array_values(array_filter(explode(',', $spec))) as $index => $item) {
        [$mode, $hold] = array_pad(explode(':', $item), 2, '0');
        if (! in_array($mode, ['stop', 'kill'], true)) {
            throw new RuntimeException("Unknown shutdown mode [$mode]; use stop or kill.");
        }
        $case = benchShutdownCase($infra, $config, $mode, (float) $hold, 'm'.$index);
        $cases[] = $case;
        echo benchShutdownCaseLine($case)."\n";
        foreach ($case['transitions'] as $line) {
            echo "      $line\n";
        }
        foreach ($case['log'] as $line) {
            echo "      workerman: $line\n";
        }
    }

    return 0;
}

/**
 * Hold a request inside Workerman in a real container, stop the container, and check
 * the held request still completes and the container exits by itself. Run it with
 * --launcher-file=<old gateway-planner-start> to see what the previous launcher did;
 * --diagnose-shutdown sends only the SIGTERM (`docker kill -s TERM`).
 */
function benchCheckShutdown(array $config, bool $diagnose = false): int
{
    echo "Preparing PostgreSQL and the seeded template...\n";
    $infra = benchInfrastructure($config);

    $case = benchShutdownCase($infra, $config, $diagnose ? 'kill' : 'stop', 7.0, 'check', [], 30);
    echo benchShutdownCaseLine($case)."\n";
    $passed = $case['completed'] && $case['exitedAt'] !== null && $case['exitCode'] === 0
        && ($case['exitedAt'] >= 6.0); // the container stayed up while the request was held
    echo $passed ? "RESULT: graceful (in-flight request drained, clean exit)\n" : "RESULT: NOT graceful\n";

    return $passed ? 0 : 1;
}
