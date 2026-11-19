<?php

declare(strict_types=1);

use Webong\Gateway\Migrations\GatewayMigrationRunner;
use Webong\Gateway\Migrations\MigrationRegistry;

it('registers canonical package paths idempotently and refuses ambiguous ownership', function (): void {
    $registry = new MigrationRegistry();
    $path = __DIR__.'/../Fixtures/extension-migrations/neutral';
    $registry->register('test/extension', $path);
    $registry->register('test/extension', $path.'/.');
    expect($registry->phinxPaths())->toHaveCount(2);
    expect(fn () => $registry->register('other/extension', $path))->toThrow(LogicException::class);
    expect(fn () => $registry->register('test/extension', __DIR__))->toThrow(LogicException::class);
    expect(fn () => $registry->register('missing', $path.'/missing'))->toThrow(InvalidArgumentException::class);
});

it('applies registered neutral migrations with the existing Phinx history and preserves data on restart', function (): void {
    $database = tempnam(sys_get_temp_dir(), 'gateway-extension-migrations-');
    try {
        $registry = new MigrationRegistry();
        $registry->register('test/extension', __DIR__.'/../Fixtures/extension-migrations/neutral');
        $runner = GatewayMigrationRunner::sqlite($database, $registry);
        expect($runner->migrate()->applied)->toContain(20261119000100);
        $pdo = new PDO('sqlite:'.$database);
        $pdo->exec("INSERT INTO extension_probes (value) VALUES ('preserved')");
        expect($runner->migrate()->applied)->toBe([])
            ->and($pdo->query('SELECT value FROM extension_probes')->fetchColumn())->toBe('preserved')
            ->and((int) $pdo->query('SELECT COUNT(*) FROM gateway_migrations WHERE version = 20261119000100')->fetchColumn())->toBe(1);
    } finally {
        unlink($database);
    }
});

it('adopts only the validated core and actually creates newly registered extension schemas', function (): void {
    $database = tempnam(sys_get_temp_dir(), 'gateway-extension-adopt-');
    try {
        $registry = new MigrationRegistry();
        GatewayMigrationRunner::sqlite($database, $registry->corePath())->migrate();
        $pdo = new PDO('sqlite:'.$database);
        $pdo->exec('DROP TABLE gateway_migrations');
        $registry->register('test/extension', __DIR__.'/../Fixtures/extension-migrations/neutral');
        $runner = GatewayMigrationRunner::sqlite($database, $registry);
        expect($runner->adoptExistingSchema()->applied)->toContain(20261119000100)
            ->and($pdo->query("SELECT name FROM sqlite_master WHERE type='table' AND name='extension_probes'")->fetchColumn())->toBe('extension_probes')
            ->and($runner->migrate()->applied)->toBe([]);
    } finally {
        unlink($database);
    }
});

it('refuses Laravel-only contributions in a neutral Symfony host before migrating core', function (): void {
    $database = tempnam(sys_get_temp_dir(), 'gateway-migration-engine-');
    try {
        $registry = new MigrationRegistry();
        $registry->registerLaravel('test/laravel', __DIR__.'/../Fixtures/extension-migrations/before');
        $command = new \Symfony\Component\Console\Tester\CommandTester(new \Webong\Gateway\Symfony\Console\MigrateGatewayCommand(
            GatewayMigrationRunner::sqlite($database, $registry), $registry,
        ));
        expect($command->execute([]))->toBe(1)
            ->and($command->getDisplay())->toContain('require a Laravel host');
        $pdo = new PDO('sqlite:'.$database);
        expect($pdo->query("SELECT name FROM sqlite_master WHERE name='gateway_migrations'")->fetchColumn())->toBeFalse();
    } finally {
        unlink($database);
    }
});
