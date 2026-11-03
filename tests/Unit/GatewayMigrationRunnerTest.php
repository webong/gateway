<?php

declare(strict_types=1);

use Webong\Gateway\Migrations\GatewayMigrationRunner;
use Illuminate\Database\Capsule\Manager as Capsule;
use Symfony\Component\Console\Tester\CommandTester;
use Webong\Gateway\Migrations\Command\MigrateGatewayCommand;

it('applies planner migrations without booting a framework', function (): void {
    $database = tempnam(sys_get_temp_dir(), 'gateway-migrations-');

    try {
        $runner = GatewayMigrationRunner::sqlite(
            database: $database,
            migrationsPath: dirname(__DIR__, 2).'/pkg/planner/migrations',
        );

        $result = $runner->migrate();

        $pdo = new PDO('sqlite:'.$database);

        expect($result->applied)->not->toBeEmpty()
            ->and($pdo->query("SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'gateway_migrations'")->fetchColumn())->toBe('gateway_migrations')
            ->and($pdo->query("SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'accounts'")->fetchColumn())->toBe('accounts');
    } finally {
        if (is_string($database) && file_exists($database)) {
            unlink($database);
        }
    }
});

it('creates the accounts schema expected by the Laravel planner', function (): void {
    $database = tempnam(sys_get_temp_dir(), 'gateway-laravel-migrations-');

    try {
        GatewayMigrationRunner::sqlite(
            database: $database,
            migrationsPath: dirname(__DIR__, 2).'/pkg/planner/migrations',
        )->migrate();

        $databaseManager = new Capsule();
        $databaseManager->addConnection([
            'driver' => 'sqlite',
            'database' => $database,
            'foreign_key_constraints' => true,
        ]);

        $columns = $databaseManager->getConnection()->select('PRAGMA table_info(accounts)');

        expect(array_column($columns, 'name'))->toBe(['id', 'token_hash', 'created_at', 'updated_at'])
            ->and($databaseManager->getConnection()->table('accounts')->insert([
                'id' => 'account-42',
                'token_hash' => str_repeat('a', 64),
            ]))->toBeTrue()
            ->and($databaseManager->getConnection()->table('accounts')->where('id', 'account-42')->value('token_hash'))
            ->toBe(str_repeat('a', 64));
    } finally {
        if (is_string($database) && file_exists($database)) {
            unlink($database);
        }
    }
});

it('adopts an existing Laravel-managed schema without replaying destructive migrations', function (): void {
    $database = tempnam(sys_get_temp_dir(), 'gateway-adopt-migrations-');

    try {
        $runner = GatewayMigrationRunner::sqlite(
            database: $database,
            migrationsPath: dirname(__DIR__, 2).'/pkg/planner/migrations',
        );
        $runner->migrate();

        $pdo = new PDO('sqlite:'.$database);
        $pdo->exec("INSERT INTO accounts (id, token_hash) VALUES ('existing-account', '".str_repeat('b', 64)."')");
        $pdo->exec('DROP TABLE gateway_migrations');

        $result = $runner->adoptExistingSchema();

        expect($result->applied)->not->toBeEmpty()
            ->and($pdo->query("SELECT token_hash FROM accounts WHERE id = 'existing-account'")->fetchColumn())
            ->toBe(str_repeat('b', 64))
            ->and((int) $pdo->query('SELECT COUNT(*) FROM gateway_migrations')->fetchColumn())
            ->toBe(count($result->applied))
            ->and($runner->migrate()->applied)->toBe([]);
    } finally {
        if (is_string($database) && file_exists($database)) {
            unlink($database);
        }
    }
});

it('refuses to adopt a schema whose required columns are incompatible', function (): void {
    $database = tempnam(sys_get_temp_dir(), 'gateway-invalid-adopt-');

    try {
        $runner = GatewayMigrationRunner::sqlite(
            database: $database,
            migrationsPath: dirname(__DIR__, 2).'/pkg/planner/migrations',
        );
        $runner->migrate();

        $pdo = new PDO('sqlite:'.$database);
        $pdo->exec('DROP TABLE gateway_migrations');
        $pdo->exec('DROP INDEX accounts_token_hash_unique');
        $pdo->exec('ALTER TABLE accounts DROP COLUMN token_hash');

        expect(fn () => $runner->adoptExistingSchema())
            ->toThrow(RuntimeException::class, 'accounts.token_hash');
    } finally {
        if (is_string($database) && file_exists($database)) {
            unlink($database);
        }
    }
});

it('uses configured planner table names for migration and adoption', function (): void {
    $database = tempnam(sys_get_temp_dir(), 'gateway-mapped-adopt-');
    $tables = [
        'servers' => 'managed_servers',
        'applications' => 'managed_applications',
        'instances' => 'managed_instances',
        'nodes' => 'managed_nodes',
        'targets' => 'managed_targets',
    ];

    try {
        $runner = GatewayMigrationRunner::sqlite(
            database: $database,
            migrationsPath: dirname(__DIR__, 2).'/pkg/planner/migrations',
            tables: $tables,
        );
        $runner->migrate();

        $pdo = new PDO('sqlite:'.$database);
        $pdo->exec('DROP TABLE gateway_migrations');

        expect($pdo->query("SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'managed_servers'")->fetchColumn())
            ->toBe('managed_servers')
            ->and($runner->adoptExistingSchema()->applied)->not->toBeEmpty();
    } finally {
        if (is_string($database) && file_exists($database)) {
            unlink($database);
        }
    }
});

it('applies and re-runs the neutral schema on PostgreSQL', function (): void {
    $database = getenv('GATEWAY_TEST_POSTGRES_DATABASE');
    if (! is_string($database) || $database === '') {
        $this->markTestSkipped('Set GATEWAY_TEST_POSTGRES_DATABASE to run PostgreSQL migration coverage.');
    }

    $runner = GatewayMigrationRunner::postgres(
        host: getenv('GATEWAY_TEST_POSTGRES_HOST') ?: '127.0.0.1',
        port: (int) (getenv('GATEWAY_TEST_POSTGRES_PORT') ?: 5432),
        database: $database,
        username: getenv('GATEWAY_TEST_POSTGRES_USERNAME') ?: 'postgres',
        password: getenv('GATEWAY_TEST_POSTGRES_PASSWORD') ?: '',
        migrationsPath: dirname(__DIR__, 2).'/pkg/planner/migrations',
    );

    $pdo = new PDO(sprintf(
        'pgsql:host=%s;port=%d;dbname=%s',
        getenv('GATEWAY_TEST_POSTGRES_HOST') ?: '127.0.0.1',
        (int) (getenv('GATEWAY_TEST_POSTGRES_PORT') ?: 5432),
        $database,
    ), getenv('GATEWAY_TEST_POSTGRES_USERNAME') ?: 'postgres', getenv('GATEWAY_TEST_POSTGRES_PASSWORD') ?: '');
    $pdo->exec('DROP SCHEMA public CASCADE');
    $pdo->exec('CREATE SCHEMA public');

    $first = $runner->migrate();
    $second = $runner->migrate();

    expect($first->applied)->not->toBeEmpty()
        ->and($second->applied)->toBe([])
        ->and($pdo->query("SELECT to_regclass('public.gateway_migrations')")->fetchColumn())->toBe('gateway_migrations')
        ->and($pdo->query("SELECT to_regclass('public.accounts')")->fetchColumn())->toBe('accounts');
});

it('applies and re-runs the neutral schema on MySQL', function (): void {
    $database = getenv('GATEWAY_TEST_MYSQL_DATABASE');
    if (! is_string($database) || $database === '') {
        $this->markTestSkipped('Set GATEWAY_TEST_MYSQL_DATABASE to run MySQL migration coverage.');
    }

    $connection = [
        'driver' => 'mysql',
        'host' => getenv('GATEWAY_TEST_MYSQL_HOST') ?: '127.0.0.1',
        'port' => (int) (getenv('GATEWAY_TEST_MYSQL_PORT') ?: 3306),
        'database' => $database,
        'username' => getenv('GATEWAY_TEST_MYSQL_USERNAME') ?: 'root',
        'password' => getenv('GATEWAY_TEST_MYSQL_PASSWORD') ?: '',
        'charset' => 'utf8mb4',
    ];
    $runner = GatewayMigrationRunner::fromConnection(
        connection: $connection,
        migrationsPath: dirname(__DIR__, 2).'/pkg/planner/migrations',
    );

    $pdo = new PDO(sprintf(
        'mysql:host=%s;port=%d;dbname=%s;charset=utf8mb4',
        $connection['host'],
        $connection['port'],
        $database,
    ), $connection['username'], $connection['password']);
    $pdo->exec('SET FOREIGN_KEY_CHECKS=0');
    foreach ($pdo->query('SHOW TABLES')->fetchAll(PDO::FETCH_COLUMN) as $table) {
        $pdo->exec(sprintf('DROP TABLE `%s`', str_replace('`', '``', (string) $table)));
    }
    $pdo->exec('SET FOREIGN_KEY_CHECKS=1');

    $first = $runner->migrate();
    $second = $runner->migrate();

    expect($first->applied)->not->toBeEmpty()
        ->and($second->applied)->toBe([])
        ->and($pdo->query("SHOW TABLES LIKE 'gateway_migrations'")->fetchColumn())->toBe('gateway_migrations')
        ->and($pdo->query("SHOW TABLES LIKE 'accounts'")->fetchColumn())->toBe('accounts');
});

it('provides the same migration runner through Symfony Console', function (): void {
    $database = tempnam(sys_get_temp_dir(), 'gateway-symfony-migrations-');

    try {
        $command = new MigrateGatewayCommand(GatewayMigrationRunner::sqlite(
            database: $database,
            migrationsPath: dirname(__DIR__, 2).'/pkg/planner/migrations',
        ));
        $tester = new CommandTester($command);

        expect($tester->execute([]))->toBe(0)
            ->and($tester->getDisplay())->toContain('Applied 1 Gateway migration.')
            ->and($tester->execute([]))->toBe(0)
            ->and($tester->getDisplay())->toContain('already current');
    } finally {
        if (is_string($database) && file_exists($database)) {
            unlink($database);
        }
    }
});
