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
