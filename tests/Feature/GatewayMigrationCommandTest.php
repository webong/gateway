<?php

declare(strict_types=1);

it('runs the framework-neutral migrations through Laravel Artisan', function (): void {
    $database = tempnam(sys_get_temp_dir(), 'gateway-artisan-migrations-');

    try {
        config()->set('database.connections.gateway_migration_test', [
            'driver' => 'sqlite',
            'database' => $database,
            'prefix' => '',
            'foreign_key_constraints' => true,
        ]);

        $this->artisan('gateway:migrate', ['--database' => 'gateway_migration_test'])
            ->assertSuccessful();

        $pdo = new PDO('sqlite:'.$database);
        expect($pdo->query("SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'gateway_migrations'")->fetchColumn())
            ->toBe('gateway_migrations');

        $this->artisan('gateway:migrate', ['--database' => 'gateway_migration_test'])
            ->assertSuccessful();
    } finally {
        if (is_string($database) && file_exists($database)) {
            unlink($database);
        }
    }
});

it('migrates the database named by a DB_URL-only PostgreSQL connection', function (): void {
    $name = getenv('GATEWAY_TEST_POSTGRES_DATABASE');
    if (! is_string($name) || $name === '') {
        $this->markTestSkipped('Set GATEWAY_TEST_POSTGRES_DATABASE to run the PostgreSQL URL migration test.');
    }
    $host = getenv('GATEWAY_TEST_POSTGRES_HOST') ?: '127.0.0.1';
    $port = (int) (getenv('GATEWAY_TEST_POSTGRES_PORT') ?: 5432);
    $username = getenv('GATEWAY_TEST_POSTGRES_USERNAME') ?: 'postgres';
    $password = getenv('GATEWAY_TEST_POSTGRES_PASSWORD') ?: '';

    $database = 'gateway_url_'.bin2hex(random_bytes(6));
    $admin = new PDO(sprintf('pgsql:host=%s;port=%d;dbname=postgres', $host, $port), $username, $password);
    $admin->exec('CREATE DATABASE "'.$database.'"');

    try {
        // Exactly how the standalone Planner is configured: a URL, with the other
        // fields left at their defaults (here an endpoint nothing listens on), as
        // in app/config.php when only DB_URL is set.
        config()->set('database.connections.gateway_url_test', [
            'driver' => 'pgsql',
            'url' => sprintf('postgresql://%s:%s@%s:%d/%s', rawurlencode($username), rawurlencode($password), $host, $port, $database),
            'host' => '127.0.0.1',
            'port' => '1',
            'database' => 'not-the-url-database',
            'username' => 'nobody',
            'password' => '',
            'charset' => 'utf8',
            'prefix' => '',
            'search_path' => 'public',
        ]);

        $this->artisan('gateway:migrate', ['--database' => 'gateway_url_test'])->assertSuccessful();

        $pdo = new PDO(sprintf('pgsql:host=%s;port=%d;dbname=%s', $host, $port, $database), $username, $password);
        expect($pdo->query("SELECT to_regclass('public.gateway_migrations')")->fetchColumn())->toBe('gateway_migrations');
    } finally {
        try {
            $admin->exec('DROP DATABASE IF EXISTS "'.$database.'" WITH (FORCE)');
        } catch (Throwable) {
            // Best-effort cleanup of the disposable database.
        }
    }
});

it('migrates and adopts the PostgreSQL schema selected by Laravel', function (): void {
    $database = getenv('GATEWAY_TEST_POSTGRES_DATABASE');
    if (! is_string($database) || $database === '') {
        $this->markTestSkipped('Set GATEWAY_TEST_POSTGRES_DATABASE to run PostgreSQL schema coverage.');
    }
    $schema = 'gateway_schema_'.bin2hex(random_bytes(6));
    $connection = [
        'driver' => 'pgsql',
        'host' => getenv('GATEWAY_TEST_POSTGRES_HOST') ?: '127.0.0.1',
        'port' => (int) (getenv('GATEWAY_TEST_POSTGRES_PORT') ?: 5432),
        'database' => $database,
        'username' => getenv('GATEWAY_TEST_POSTGRES_USERNAME') ?: 'postgres',
        'password' => getenv('GATEWAY_TEST_POSTGRES_PASSWORD') ?: '',
        'search_path' => [$schema, 'public'],
        'sslmode' => 'disable',
    ];
    config()->set('database.connections.gateway_schema_test', $connection);
    $db = app('db')->connection('gateway_schema_test');
    $db->statement('CREATE SCHEMA "'.$schema.'"');
    try {
        $this->artisan('gateway:migrate', ['--database' => 'gateway_schema_test'])->assertSuccessful();
        expect($db->selectOne('SELECT current_schema() AS name')->name)->toBe($schema)
            ->and($db->getSchemaBuilder()->hasTable('accounts'))->toBeTrue()
            ->and($db->selectOne("SELECT to_regclass('{$schema}.gateway_migrations') AS name")->name)->not->toBeNull();
        $db->table('accounts')->insert(['id' => 'preserved', 'token_hash' => str_repeat('c', 64)]);
        $db->statement('DROP TABLE "'.$schema.'".gateway_migrations');
        $this->artisan('gateway:migrate', ['--database' => 'gateway_schema_test', '--adopt-existing' => true])->assertSuccessful();
        $this->artisan('gateway:migrate', ['--database' => 'gateway_schema_test'])->assertSuccessful();
        expect($db->table('accounts')->where('id', 'preserved')->value('token_hash'))->toBe(str_repeat('c', 64));
    } finally {
        $db->statement('DROP SCHEMA "'.$schema.'" CASCADE');
        app('db')->purge('gateway_schema_test');
    }
});

it('does not discard PostgreSQL TLS policy during Laravel migrations', function (): void {
    $database = getenv('GATEWAY_TEST_POSTGRES_DATABASE');
    if (! is_string($database) || $database === '') {
        $this->markTestSkipped('Set GATEWAY_TEST_POSTGRES_DATABASE to run PostgreSQL TLS policy coverage.');
    }
    config()->set('database.connections.gateway_tls_test', [
        'driver' => 'pgsql',
        'host' => getenv('GATEWAY_TEST_POSTGRES_HOST') ?: '127.0.0.1',
        'port' => (int) (getenv('GATEWAY_TEST_POSTGRES_PORT') ?: 5432),
        'database' => $database,
        'username' => getenv('GATEWAY_TEST_POSTGRES_USERNAME') ?: 'postgres',
        'password' => getenv('GATEWAY_TEST_POSTGRES_PASSWORD') ?: '',
        // libpq must reject this policy, rather than silently using its default.
        'sslmode' => 'invalid-policy',
    ]);
    try {
        expect(fn () => $this->artisan('gateway:migrate', ['--database' => 'gateway_tls_test']))
            ->toThrow(PDOException::class, 'invalid sslmode value');
    } finally {
        app('db')->purge('gateway_tls_test');
    }
});

it('migrates and adopts the MySQL database configured by Laravel', function (): void {
    $name = getenv('GATEWAY_TEST_MYSQL_DATABASE');
    if (! is_string($name) || $name === '') {
        $this->markTestSkipped('Set GATEWAY_TEST_MYSQL_DATABASE to run MySQL migration coverage.');
    }
    $connection = [
        'driver' => 'mysql',
        'host' => getenv('GATEWAY_TEST_MYSQL_HOST') ?: '127.0.0.1',
        'port' => (int) (getenv('GATEWAY_TEST_MYSQL_PORT') ?: 3306),
        'database' => $name,
        'username' => getenv('GATEWAY_TEST_MYSQL_USERNAME') ?: 'root',
        'password' => getenv('GATEWAY_TEST_MYSQL_PASSWORD') ?: '',
        'charset' => 'utf8mb4',
    ];
    $admin = new PDO(sprintf('mysql:host=%s;port=%d;dbname=%s', $connection['host'], $connection['port'], $name), $connection['username'], $connection['password']);
    $database = 'gateway_mysql_'.bin2hex(random_bytes(6));
    $admin->exec('CREATE DATABASE `'.$database.'`');
    $connection['database'] = $database;
    config()->set('database.connections.gateway_mysql_test', $connection);
    $db = app('db')->connection('gateway_mysql_test');
    try {
        $this->artisan('gateway:migrate', ['--database' => 'gateway_mysql_test'])->assertSuccessful();
        $db->table('accounts')->insert(['id' => 'mysql-preserved', 'token_hash' => str_repeat('d', 64)]);
        $db->statement('DROP TABLE gateway_migrations');
        $this->artisan('gateway:migrate', ['--database' => 'gateway_mysql_test', '--adopt-existing' => true])->assertSuccessful();
        $this->artisan('gateway:migrate', ['--database' => 'gateway_mysql_test'])->assertSuccessful();
        expect($db->table('accounts')->where('id', 'mysql-preserved')->value('token_hash'))->toBe(str_repeat('d', 64));
    } finally {
        app('db')->purge('gateway_mysql_test');
        $admin->exec('DROP DATABASE `'.$database.'`');
    }
});

it('migrates the same in-memory SQLite connection used by Laravel', function (): void {
    config()->set('database.connections.gateway_memory_test', [
        'driver' => 'sqlite',
        'database' => ':memory:',
        'prefix' => '',
        'foreign_key_constraints' => true,
    ]);
    try {
        $db = app('db')->connection('gateway_memory_test');
        $this->artisan('gateway:migrate', ['--database' => 'gateway_memory_test'])->assertSuccessful();
        expect($db->getSchemaBuilder()->hasTable('accounts'))->toBeTrue()
            ->and($db->getSchemaBuilder()->hasTable('gateway_migrations'))->toBeTrue();
        $this->artisan('gateway:migrate', ['--database' => 'gateway_memory_test'])->assertSuccessful();
    } finally {
        app('db')->purge('gateway_memory_test');
    }
});
