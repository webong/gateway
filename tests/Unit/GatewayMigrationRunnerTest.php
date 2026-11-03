<?php

declare(strict_types=1);

use Webong\Gateway\Migrations\GatewayMigrationRunner;
use Illuminate\Database\Capsule\Manager as Capsule;

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
