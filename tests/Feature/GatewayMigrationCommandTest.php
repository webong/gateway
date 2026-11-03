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
