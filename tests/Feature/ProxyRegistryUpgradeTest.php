<?php

declare(strict_types=1);

use Illuminate\Database\QueryException;
use Illuminate\Support\Facades\Artisan;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Schema;
use Illuminate\Support\Str;
use Webong\Proxy\Database\SubscriptionIdentity;

/*
 * Proxy v0.3.1 replaced the seven-column subscription unique index (which
 * MySQL's 3,072-byte limit rejects) with a fixed-size registration hash. These
 * tests prove what an operator relies on when they run the same
 * `artisan migrate --realpath --path=vendor/webong/proxy/database/migrations`
 * that scripts/planner-start and scripts/router-start run:
 *
 *  - a fresh registry gets the hash and the unique index it replaces;
 *  - a registry created by Proxy v0.2.0 keeps every subscription through the
 *    one additive migration, and only that migration runs;
 *  - MySQL 8.4 with its default utf8mb4 now accepts the Proxy migrations.
 *
 * The MySQL case is skipped unless GATEWAY_TEST_MYSQL_DATABASE is set, like the
 * Planner's own MySQL migration test. The Planner's MySQL runtime is covered by
 * PlannerMysqlRuntimeTest.
 */

const PROXY_PORTABLE_IDENTITY_MIGRATION = '2026_11_12_000001_use_portable_subscription_identity';

function proxyMigrationsPath(): string
{
    $path = realpath(dirname((string) (new ReflectionClass(Webong\Proxy\WebProxyServiceProvider::class))->getFileName()).'/../database/migrations');
    expect($path)->toBeString();

    return $path;
}

/**
 * Create an isolated registry on the named driver, or skip when the driver's
 * disposable server is not configured. Returns the connection name and a teardown.
 *
 * @return array{string, Closure(): void}
 */
function isolatedProxyRegistry(object $test, string $driver): array
{
    $name = 'proxy_registry_'.bin2hex(random_bytes(4));
    $cleanup = static function () use ($name): void {
        app('db')->purge($name);
    };

    if ($driver === 'sqlite') {
        $file = tempnam(sys_get_temp_dir(), 'proxy-registry-');
        config()->set("database.connections.{$name}", ['driver' => 'sqlite', 'database' => $file, 'prefix' => '', 'foreign_key_constraints' => true]);

        return [$name, static function () use ($cleanup, $file): void {
            $cleanup();
            @unlink($file);
        }];
    }

    if ($driver === 'pgsql') {
        $database = getenv('GATEWAY_TEST_POSTGRES_DATABASE');
        if (! is_string($database) || $database === '') {
            $test->markTestSkipped('Set GATEWAY_TEST_POSTGRES_DATABASE to run PostgreSQL registry coverage.');
        }
        $schema = 'proxy_registry_'.bin2hex(random_bytes(6));
        config()->set("database.connections.{$name}", [
            'driver' => 'pgsql',
            'host' => getenv('GATEWAY_TEST_POSTGRES_HOST') ?: '127.0.0.1',
            'port' => (int) (getenv('GATEWAY_TEST_POSTGRES_PORT') ?: 5432),
            'database' => $database,
            'username' => getenv('GATEWAY_TEST_POSTGRES_USERNAME') ?: 'postgres',
            'password' => getenv('GATEWAY_TEST_POSTGRES_PASSWORD') ?: '',
            'search_path' => [$schema, 'public'],
            'sslmode' => 'disable',
        ]);
        DB::connection($name)->statement('CREATE SCHEMA "'.$schema.'"');

        return [$name, static function () use ($name, $schema, $cleanup): void {
            DB::connection($name)->statement('DROP SCHEMA "'.$schema.'" CASCADE');
            $cleanup();
        }];
    }

    $admin = getenv('GATEWAY_TEST_MYSQL_DATABASE');
    if (! is_string($admin) || $admin === '') {
        $test->markTestSkipped('Set GATEWAY_TEST_MYSQL_DATABASE to run MySQL registry coverage.');
    }
    $connection = [
        'driver' => 'mysql',
        'host' => getenv('GATEWAY_TEST_MYSQL_HOST') ?: '127.0.0.1',
        'port' => (int) (getenv('GATEWAY_TEST_MYSQL_PORT') ?: 3306),
        'database' => $admin,
        'username' => getenv('GATEWAY_TEST_MYSQL_USERNAME') ?: 'root',
        'password' => getenv('GATEWAY_TEST_MYSQL_PASSWORD') ?: '',
        // Laravel's stock MySQL connection, which is what a Gateway host would get.
        'charset' => 'utf8mb4',
        'collation' => 'utf8mb4_unicode_ci',
        'prefix' => '',
        'strict' => true,
        'engine' => null,
    ];
    $database = 'proxy_registry_'.bin2hex(random_bytes(6));
    config()->set("database.connections.{$name}_admin", $connection);
    DB::connection("{$name}_admin")->statement('CREATE DATABASE `'.$database.'` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci');
    config()->set("database.connections.{$name}", [...$connection, 'database' => $database]);

    return [$name, static function () use ($name, $database, $cleanup): void {
        $cleanup();
        DB::connection("{$name}_admin")->statement('DROP DATABASE `'.$database.'`');
        app('db')->purge("{$name}_admin");
    }];
}

/** @return string Artisan output. */
function migrateProxyRegistry(string $connection, string $path): string
{
    $status = Artisan::call('migrate', ['--database' => $connection, '--path' => [$path], '--realpath' => true, '--force' => true]);
    $output = Artisan::output();
    expect($status)->toBe(0, $output);

    return $output;
}

/** @param array<string, mixed> $overrides @return array<string, mixed> */
function legacySubscription(array $overrides = []): array
{
    return [
        'id' => (string) Str::uuid(),
        'endpoint_id' => '4f6e1c22-0b6a-4a52-9a0e-6b0f0d8f2a11',
        'owner_id' => 'owner-1',
        'registration_id' => 'registration-1',
        'webhook_group' => 'meta',
        'routing_scope' => 'application',
        'routing_key' => 'app-123',
        'target_type' => 'request',
        'target' => 'https://subscriber.example.test/hook',
        'metadata' => json_encode(['source' => 'legacy', 'attempt' => 3], JSON_THROW_ON_ERROR),
        'is_active' => true,
        'last_delivered_at' => '2026-10-01 08:30:00',
        'last_failed_at' => null,
        'last_error' => null,
        'created_at' => '2026-09-30 10:00:00',
        'updated_at' => '2026-10-01 08:30:00',
        ...$overrides,
    ];
}

/** @return list<array<string, mixed>> */
function legacySubscriptions(): array
{
    return [
        legacySubscription(),
        legacySubscription(['registration_id' => 'registration-2', 'owner_id' => 'owner-2', 'is_active' => false, 'last_error' => 'HTTP 503']),
        // Collation- and whitespace-distinct tuples stay distinct.
        legacySubscription(['registration_id' => 'Registration-1']),
        legacySubscription(['registration_id' => 'registration-1 ']),
        // Unicode survives, and so do the longest values the columns accept: the
        // seven 255-character columns are exactly what exceeded MySQL's limit.
        legacySubscription(['owner_id' => 'Ünïcode-Éndpoint-用户-🙂', 'registration_id' => 'Ünïcode-Éndpoint-用户-🙂']),
        legacySubscription([
            'owner_id' => str_repeat('o', 255), 'registration_id' => str_repeat('r', 255), 'webhook_group' => str_repeat('g', 255),
            'routing_scope' => str_repeat('s', 255), 'routing_key' => str_repeat('k', 255), 'target_type' => str_repeat('t', 255),
        ]),
    ];
}

/** @return list<array<string, mixed>> */
function subscriptionSnapshot(string $connection): array
{
    return DB::connection($connection)->table('subscriptions')->orderBy('id')->get()
        ->map(static function (object $row): array {
            $row = (array) $row;
            unset($row['registration_hash']);
            $row['metadata'] = is_string($row['metadata']) ? json_decode($row['metadata'], true) : $row['metadata'];
            $row['is_active'] = (bool) $row['is_active'];

            return $row;
        })->all();
}

function assertPortableSubscriptionIdentity(string $connection): void
{
    $indexes = collect(Schema::connection($connection)->getIndexes('subscriptions'));
    $unique = $indexes->where('unique', true)->where('primary', false);

    expect(Schema::connection($connection)->hasColumn('subscriptions', 'registration_hash'))->toBeTrue()
        ->and($unique->pluck('columns')->all())->toBe([['registration_hash']])
        ->and($indexes->pluck('name')->all())->not->toContain('subscriptions_registration_unique')
        ->and($indexes->pluck('name')->all())->not->toContain('web_proxy_destinations_registration_unique')
        ->and($indexes->pluck('name')->all())->toContain('subscriptions_routing_index');
}

dataset('registry servers', ['sqlite', 'pgsql']);
dataset('registry servers for a fresh install', ['sqlite', 'pgsql', 'mysql']);

it('creates a portable subscription identity on a fresh registry', function (string $driver): void {
    [$connection, $cleanup] = isolatedProxyRegistry($this, $driver);
    try {
        $output = migrateProxyRegistry($connection, proxyMigrationsPath());
        expect($output)->toContain(PROXY_PORTABLE_IDENTITY_MIGRATION)->toContain('create_subscriptions_table');
        assertPortableSubscriptionIdentity($connection);

        $table = DB::connection($connection)->table('subscriptions');
        foreach (legacySubscriptions() as $row) {
            $table->insert($row + ['registration_hash' => SubscriptionIdentity::hash($row)]);
        }
        expect($table->count())->toBe(count(legacySubscriptions()))
            ->and($table->where('owner_id', str_repeat('o', 255))->count())->toBe(1);

        // The same registration cannot be stored twice, even with a new row id.
        $duplicate = legacySubscription(['id' => (string) Str::uuid()]);
        expect(fn () => DB::connection($connection)->table('subscriptions')->insert($duplicate + ['registration_hash' => SubscriptionIdentity::hash($duplicate)]))
            ->toThrow(QueryException::class);

        expect(migrateProxyRegistry($connection, proxyMigrationsPath()))->toContain('Nothing to migrate');
    } finally {
        $cleanup();
    }
})->with('registry servers for a fresh install');

it('upgrades a registry created by Proxy 0.2.0 without losing a subscription', function (string $driver): void {
    [$connection, $cleanup] = isolatedProxyRegistry($this, $driver);
    try {
        $legacy = migrateProxyRegistry($connection, __DIR__.'/../Fixtures/proxy-0.2.0-migrations');
        expect($legacy)->toContain('create_subscriptions_table')->not->toContain(PROXY_PORTABLE_IDENTITY_MIGRATION);
        $before = Schema::connection($connection)->getIndexes('subscriptions');
        expect(collect($before)->pluck('name')->all())->toContain('subscriptions_registration_unique')
            ->and(Schema::connection($connection)->hasColumn('subscriptions', 'registration_hash'))->toBeFalse();

        foreach (legacySubscriptions() as $row) {
            DB::connection($connection)->table('subscriptions')->insert($row);
        }
        $snapshot = subscriptionSnapshot($connection);
        expect($snapshot)->toHaveCount(count(legacySubscriptions()));

        // The same command scripts/planner-start and scripts/router-start run.
        $upgrade = migrateProxyRegistry($connection, proxyMigrationsPath());
        expect($upgrade)->toContain(PROXY_PORTABLE_IDENTITY_MIGRATION)
            ->not->toContain('create_endpoints_table')->not->toContain('create_subscriptions_table')
            ->not->toContain('create_profiles_table')->not->toContain('create_endpoint_registrations_table');

        assertPortableSubscriptionIdentity($connection);
        expect(subscriptionSnapshot($connection))->toBe($snapshot);
        $rows = DB::connection($connection)->table('subscriptions')->orderBy('id')->get();
        foreach ($rows as $row) {
            expect($row->registration_hash)->toMatch('/^[0-9a-f]{64}$/')
                ->and($row->registration_hash)->toBe(SubscriptionIdentity::hash((array) $row));
        }
        expect($rows->pluck('registration_hash')->unique())->toHaveCount($rows->count());

        // Writers must be stopped across the upgrade: code that predates the hash can no longer insert.
        $oldWriter = legacySubscription(['id' => (string) Str::uuid(), 'registration_id' => 'written-by-old-code']);
        expect(fn () => DB::connection($connection)->table('subscriptions')->insert($oldWriter))->toThrow(QueryException::class);

        // The registry keeps rejecting a registration that already exists.
        $existing = (array) $rows->first();
        $existing['id'] = (string) Str::uuid();
        expect(fn () => DB::connection($connection)->table('subscriptions')->insert($existing))->toThrow(QueryException::class);

        expect(migrateProxyRegistry($connection, proxyMigrationsPath()))->toContain('Nothing to migrate');
        expect(subscriptionSnapshot($connection))->toBe($snapshot);
    } finally {
        $cleanup();
    }
})->with('registry servers');

it('refuses to roll the portable identity back on MySQL', function (): void {
    [$connection, $cleanup] = isolatedProxyRegistry($this, 'mysql');
    try {
        migrateProxyRegistry($connection, proxyMigrationsPath());
        $row = legacySubscription();
        DB::connection($connection)->table('subscriptions')->insert($row + ['registration_hash' => SubscriptionIdentity::hash($row)]);

        expect(fn () => Artisan::call('migrate:rollback', ['--database' => $connection, '--path' => [proxyMigrationsPath()], '--realpath' => true, '--step' => 1, '--force' => true]))
            ->toThrow(RuntimeException::class, 'cannot be rolled back on MySQL/MariaDB');
        assertPortableSubscriptionIdentity($connection);
        expect(DB::connection($connection)->table('subscriptions')->count())->toBe(1);
    } finally {
        $cleanup();
    }
});

it('runs the Proxy migrations on the default utf8mb4 MySQL configuration', function (): void {
    [$connection, $cleanup] = isolatedProxyRegistry($this, 'mysql');
    try {
        migrateProxyRegistry($connection, proxyMigrationsPath());

        $charset = DB::connection($connection)->selectOne('SELECT @@character_set_database AS charset, @@collation_database AS collation');
        $table = DB::connection($connection)->selectOne('SELECT table_collation AS collation FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?', ['subscriptions']);
        expect($charset->charset)->toBe('utf8mb4')
            ->and($table->collation)->toStartWith('utf8mb4_');
    } finally {
        $cleanup();
    }
});

it('shows why Proxy 0.2.0 could not run on MySQL', function (): void {
    // The reason for the change: the original seven-column key is 7 x 255 utf8mb4
    // characters, beyond InnoDB's 3,072-byte index limit (error 1071).
    [$connection, $cleanup] = isolatedProxyRegistry($this, 'mysql');
    try {
        expect(fn () => Artisan::call('migrate', ['--database' => $connection, '--path' => [__DIR__.'/../Fixtures/proxy-0.2.0-migrations'], '--realpath' => true, '--force' => true]))
            ->toThrow(QueryException::class, 'Specified key was too long');
    } finally {
        $cleanup();
    }
});
