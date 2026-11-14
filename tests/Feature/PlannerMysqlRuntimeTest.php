<?php

declare(strict_types=1);

use Illuminate\Support\Carbon;
use Illuminate\Support\Facades\Artisan;
use Illuminate\Support\Facades\Cache;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Str;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Tests\Support\TestPathResolver;
use Webong\Proxy\EndpointDefinition;
use Webong\Proxy\EnsureEndpoint;
use Webong\Proxy\ProfileDefinition;

/*
 * The Planner runs on MySQL 8.4 with the `mysql` connection in app/planner/config.php, the
 * same configuration scripts/planner-start and scripts/router-start use. These tests
 * migrate a disposable database the way those launchers do (`gateway:migrate`, then
 * Proxy's migrations) and prove what differs from PostgreSQL and SQLite:
 *
 *  - the registry serves a subscription and plans a delivery;
 *  - comparisons are exact (case, trailing spaces), as on the other drivers;
 *  - timestamps are not capped at 2038;
 *  - stored JSON keeps its meaning, though MySQL rewrites its text;
 *  - the database cache store's locks work;
 *  - the connection is READ COMMITTED and UTC.
 *
 * The Router's shared delivery queue stays PostgreSQL-only; see docs/deployment.md.
 * They are skipped unless GATEWAY_TEST_MYSQL_DATABASE names a MySQL 8.4 server, like
 * the Planner's MySQL migration test.
 */

function plannerMysqlProxyMigrations(): string
{
    $path = realpath(dirname((string) (new ReflectionClass(Webong\Proxy\WebProxyServiceProvider::class))->getFileName()).'/../database/migrations');
    expect($path)->toBeString();

    return $path;
}

/**
 * Create a disposable database, point Laravel's default connection at it through the
 * real app/planner/config.php `mysql` connection (read from DB_URL, as the launchers do), and
 * migrate it. Returns the connection name and a teardown.
 *
 * @return array{string, Closure(): void}
 */
function plannerMysqlRuntime(object $test, string $databaseCollation = 'utf8mb4_unicode_ci'): array
{
    $admin = getenv('GATEWAY_TEST_MYSQL_DATABASE');
    if (! is_string($admin) || $admin === '') {
        $test->markTestSkipped('Set GATEWAY_TEST_MYSQL_DATABASE to run the Planner MySQL runtime coverage.');
    }
    $host = getenv('GATEWAY_TEST_MYSQL_HOST') ?: '127.0.0.1';
    $port = (int) (getenv('GATEWAY_TEST_MYSQL_PORT') ?: 3306);
    $username = getenv('GATEWAY_TEST_MYSQL_USERNAME') ?: 'root';
    $password = getenv('GATEWAY_TEST_MYSQL_PASSWORD') ?: '';

    config()->set('database.connections.planner_mysql_admin', [
        'driver' => 'mysql', 'host' => $host, 'port' => $port, 'database' => $admin,
        'username' => $username, 'password' => $password,
        'charset' => 'utf8mb4', 'collation' => 'utf8mb4_unicode_ci', 'strict' => true,
    ]);
    $database = 'gateway_runtime_'.bin2hex(random_bytes(6));
    DB::connection('planner_mysql_admin')->statement(
        'CREATE DATABASE `'.$database.'` CHARACTER SET utf8mb4 COLLATE '.$databaseCollation,
    );

    $previousUrl = getenv('DB_URL');
    putenv('DB_URL='.sprintf('mysql://%s:%s@%s:%d/%s', rawurlencode($username), rawurlencode($password), $host, $port, $database));
    $connection = (require dirname(__DIR__, 2).'/app/planner/config.php')['database']['connections']['mysql'];

    $name = 'planner_mysql';
    $previousDefault = config('database.default');
    config()->set("database.connections.{$name}", $connection);
    config()->set('database.default', $name);
    DB::setDefaultConnection($name);

    $teardown = static function () use ($name, $previousDefault, $previousUrl, $database): void {
        app('db')->purge($name);
        config()->set('database.default', $previousDefault);
        DB::setDefaultConnection($previousDefault);
        putenv($previousUrl === false ? 'DB_URL' : 'DB_URL='.$previousUrl);
        DB::connection('planner_mysql_admin')->statement('DROP DATABASE IF EXISTS `'.$database.'`');
        app('db')->purge('planner_mysql_admin');
    };

    try {
        expect(Artisan::call('gateway:migrate', ['--database' => $name]))->toBe(0, Artisan::output());
        expect(Artisan::call('migrate', [
            '--database' => $name, '--path' => [plannerMysqlProxyMigrations()], '--realpath' => true, '--force' => true,
        ]))->toBe(0, Artisan::output());
    } catch (Throwable $failure) {
        $teardown();

        throw $failure;
    }

    return [$name, $teardown];
}

it('serves a subscription and plans a delivery on MySQL', function (): void {
    [$name, $teardown] = plannerMysqlRuntime($this);

    try {
        // The launchers' configuration, as MySQL sees it.
        $session = DB::selectOne('SELECT @@session.transaction_isolation AS isolation, @@session.time_zone AS zone, @@collation_connection AS collation, DATABASE() AS name');
        expect($session->isolation)->toBe('READ-COMMITTED')
            ->and($session->zone)->toBe('+00:00')
            ->and($session->collation)->toBe('utf8mb4_0900_bin')
            ->and(DB::selectOne('SELECT NOW() = UTC_TIMESTAMP() AS utc')->utc)->toEqual(1);

        $endpoint = app(EnsureEndpoint::class)->handle(new EndpointDefinition(
            client: 'relay-test',
            externalId: 'provider-app-mysql',
            signingSecret: 'provider-secret',
            verificationToken: 'verification-token',
            endpointKey: 'mysql-endpoint',
            credentialOwnerId: 'endpoint-owner',
            managed: false,
        ));
        TestPathResolver::$endpointKey = $endpoint->record->endpoint_key;
        $endpoint->registerProfile(new ProfileDefinition(name: 'egress', configuration: [
            'proxy' => ['https' => 'http://gateway:p%40ss@proxy.example.test:8080', 'no' => ['.internal.example.test']],
            'options' => ['connect_timeout' => 5, 'timeout' => 15],
        ], driver: 'guzzle'));

        $this->withToken('registry-secret')->postJson('/registry/endpoints/'.$endpoint->record->endpoint_key.'/subscriptions', [
            'subscriber_id' => 'mysql-subscriber',
            'group' => 'meta',
            'routing_scope' => 'application',
            'routing_key' => 'app-123',
            'url' => 'https://example.com/hook',
            'profile' => 'egress',
        ])->assertCreated();

        $plan = app(Webong\Gateway\Web\RegistryRoutePlanner::class)->plan(new IngressRequest(
            deliveryId: 'mysql-delivery', method: 'POST', host: 'relay.example.test', path: '/provider/events/app-123',
            rawQuery: 'source=meta', headers: ['Content-Type' => ['application/json']], body: '{"event":"created"}',
        ));
        expect($plan->relays)->toHaveCount(1)
            ->and(json_decode(json_encode($plan->relays[0]->toArray(), JSON_THROW_ON_ERROR), true, flags: JSON_THROW_ON_ERROR))->toBeArray();

        // The subscription is in the MySQL database, not in the sqlite fixture database.
        expect(DB::connection($name)->table('subscriptions')->count())->toBe(1);

        // Running the migrations again is a no-op, as on a second container start.
        expect(Artisan::call('gateway:migrate', ['--database' => $name]))->toBe(0);
    } finally {
        $teardown();
    }
});

it('compares keys exactly on MySQL, whatever the database default collation is', function (): void {
    // The database default is the case-insensitive one a stock MySQL server would give.
    [$name, $teardown] = plannerMysqlRuntime($this, 'utf8mb4_0900_ai_ci');

    try {
        $db = DB::connection($name);
        $collations = $db->select(
            "SELECT table_name AS name, table_collation AS collation FROM information_schema.tables
             WHERE table_schema = DATABASE() AND table_name NOT IN ('gateway_migrations', 'migrations')
               AND table_name IN ('accounts', 'targets', 'servers', 'configurations', 'schedules', 'deliveries', 'events', 'delivery_admin_endpoints')",
        );
        expect($collations)->not->toBeEmpty();
        foreach ($collations as $table) {
            expect($table->collation)->toBe('utf8mb4_0900_bin', "{$table->name} is not exact");
        }

        $target = static fn (string $name): array => [
            'id' => (string) Str::uuid(), 'name' => $name, 'driver' => 'ssh', 'connection' => '{}',
            'worker_token_hash' => str_repeat('a', 64),
        ];
        // PostgreSQL and SQLite accept all three as distinct names; MySQL's stock collation would not.
        $db->table('targets')->insert([$target('Alpha'), $target('alpha'), $target('alpha ')]);
        expect($db->table('targets')->where('name', 'alpha')->count())->toBe(1)
            ->and($db->table('targets')->count())->toBe(3);

        $db->table('accounts')->insert([
            ['id' => 'lower', 'token_hash' => str_repeat('a', 64)],
            ['id' => 'upper', 'token_hash' => str_repeat('A', 64)],
        ]);
        expect($db->table('accounts')->where('token_hash', str_repeat('a', 64))->value('id'))->toBe('lower');
    } finally {
        $teardown();
    }
});

it('stores dates after 2038 and JSON that keeps its meaning on MySQL', function (): void {
    [$name, $teardown] = plannerMysqlRuntime($this);

    try {
        $db = DB::connection($name);
        // MySQL's TIMESTAMP ends at 2038-01-19; Gateway's columns are DATETIME.
        $types = $db->select(
            "SELECT DISTINCT data_type AS type FROM information_schema.columns
             WHERE table_schema = DATABASE() AND table_name IN ('schedules', 'deliveries', 'instances', 'nodes', 'events')
               AND column_name IN ('next_run_at', 'lease_until', 'next_attempt_at', 'idempotency_expires_at', 'heartbeat_at', 'received_at')",
        );
        expect(array_column($types, 'type'))->toBe(['datetime']);

        $far = '2090-06-15 12:00:00';
        $db->table('schedules')->insert([
            'id' => (string) Str::uuid(), 'name' => 'yearly', 'cron' => '0 0 1 1 *', 'timezone' => 'UTC',
            'language' => 'lua', 'source' => '', 'payload' => '{}', 'next_run_at' => $far,
        ]);
        expect($db->table('schedules')->value('next_run_at'))->toBe($far);

        // MySQL's JSON type normalises the text (key order, whitespace, escaped slashes). Nothing
        // Gateway stores is hashed or signed from the stored text: bodies are kept as base64 inside it.
        $body = random_bytes(64);
        $payload = ['z' => 1, 'a' => ['url' => 'https://example.test/a/b', 'body_base64' => base64_encode($body), 'truncated' => false]];
        $text = '{"z": 1,  "a": {"url":"https:\/\/example.test\/a\/b","body_base64":"'.base64_encode($body).'","truncated":false}}';
        $db->table('deliveries')->insert([
            'id' => str_repeat('d', 32), 'payload' => $text, 'state' => 'queued', 'next_attempt_at' => $far,
        ]);
        $stored = (string) $db->table('deliveries')->where('id', str_repeat('d', 32))->value('payload');
        expect(json_decode($stored, true, flags: JSON_THROW_ON_ERROR))->toEqual($payload)
            ->and(base64_decode(json_decode($stored, true)['a']['body_base64'], true))->toBe($body);
    } finally {
        $teardown();
    }
});

it('takes and releases database cache locks on MySQL', function (): void {
    [$name, $teardown] = plannerMysqlRuntime($this);

    try {
        // The launchers' shared Planner store: GATEWAY_CACHE_STORE=database on the Planner connection.
        config()->set('cache.stores.gateway_mysql', [
            'driver' => 'database', 'connection' => $name, 'table' => 'gateway_cache', 'lock_table' => 'gateway_cache_locks',
        ]);
        $store = Cache::store('gateway_mysql');

        $store->put('route-generation', 7, 60);
        expect($store->get('route-generation'))->toBe(7);

        $first = $store->lock('subscription:mutation', 30);
        $second = $store->lock('subscription:mutation', 30);
        expect($first->get())->toBeTrue()
            ->and($second->get())->toBeFalse();
        $first->release();
        expect($second->get())->toBeTrue();
        $second->release();

        // A lock abandoned by a crashed worker is taken over once it has expired.
        $abandoned = $store->lock('subscription:abandoned', 5);
        expect($abandoned->get())->toBeTrue();
        Carbon::setTestNow(Carbon::now()->addSeconds(10));
        try {
            expect($store->lock('subscription:abandoned', 5)->get())->toBeTrue();
        } finally {
            Carbon::setTestNow();
        }
    } finally {
        $teardown();
    }
});

it('gives Proxy\'s tables exact comparisons when the database is created with the exact collation', function (): void {
    // docs/deployment.md tells operators to create the database this way: Proxy's own tables
    // inherit the database default, while Gateway's tables always set theirs.
    [$name, $teardown] = plannerMysqlRuntime($this, 'utf8mb4_0900_bin');

    try {
        $inexact = DB::connection($name)->select(
            "SELECT table_name AS name, table_collation AS collation FROM information_schema.tables
             WHERE table_schema = DATABASE() AND table_name <> 'gateway_migrations' AND table_collation <> 'utf8mb4_0900_bin'",
        );
        expect($inexact)->toBe([]);
    } finally {
        $teardown();
    }
});
