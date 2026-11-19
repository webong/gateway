<?php

declare(strict_types=1);

use Webong\Gateway\Migrations\MigrationRegistry;

it('coordinates prerequisite core and extension schemas on the selected connection with native histories', function (): void {
    $file = tempnam(sys_get_temp_dir(), 'gateway-extension-command-');
    $registry = app(MigrationRegistry::class);
    $registry->registerLaravel('test/prerequisite', __DIR__.'/../Fixtures/extension-migrations/before', beforeCore: true);
    $registry->register('test/neutral', __DIR__.'/../Fixtures/extension-migrations/neutral');
    $registry->registerLaravel('test/extension', __DIR__.'/../Fixtures/extension-migrations/after');
    config()->set('database.connections.extension_test', ['driver' => 'sqlite', 'database' => $file, 'prefix' => '', 'foreign_key_constraints' => true]);
    try {
        $this->artisan('gateway:migrate', ['--database' => 'extension_test'])->assertSuccessful();
        $pdo = new PDO('sqlite:'.$file);
        $pdo->exec("INSERT INTO registered_extension_rows (value) VALUES ('retained')");
        $this->artisan('gateway:migrate', ['--database' => 'extension_test'])->assertSuccessful();
        expect($pdo->query('SELECT value FROM registered_extension_rows')->fetchColumn())->toBe('retained')
            ->and((int) $pdo->query("SELECT COUNT(*) FROM migrations WHERE migration = '2026_11_19_000001_create_adapter_prerequisite'")->fetchColumn())->toBe(1)
            ->and((int) $pdo->query('SELECT COUNT(*) FROM gateway_migrations WHERE version = 20261119000100')->fetchColumn())->toBe(1)
            ->and($pdo->query("SELECT name FROM sqlite_master WHERE name='endpoints' AND type='table'")->fetchColumn())->toBe('endpoints');
    } finally {
        app('db')->purge('extension_test');
        unlink($file);
    }
});

it('does not run core or later extensions after a prerequisite migration fails', function (): void {
    $file = tempnam(sys_get_temp_dir(), 'gateway-extension-failure-');
    app(MigrationRegistry::class)->registerLaravel('test/failure', __DIR__.'/../Fixtures/extension-migrations/failure', beforeCore: true);
    config()->set('database.connections.extension_failure', ['driver' => 'sqlite', 'database' => $file, 'prefix' => '']);
    try {
        expect(fn () => $this->artisan('gateway:migrate', ['--database' => 'extension_failure'])->run())
            ->toThrow(RuntimeException::class, 'Prerequisite migration rejected.');
        $pdo = new PDO('sqlite:'.$file);
        expect($pdo->query("SELECT name FROM sqlite_master WHERE name='gateway_migrations'")->fetchColumn())->toBeFalse();
    } finally {
        app('db')->purge('extension_failure');
        unlink($file);
    }
});

it('honours published prerequisite overrides without moving unrelated application migrations before core', function (): void {
    $file = tempnam(sys_get_temp_dir(), 'gateway-override-command-');
    $databasePath = sys_get_temp_dir().'/gateway-published-migrations-'.bin2hex(random_bytes(6));
    $originalPath = database_path();
    mkdir($databasePath.'/migrations', 0777, true);
    $override = $databasePath.'/migrations/2026_11_19_000001_create_adapter_prerequisite.php';
    file_put_contents($override, <<<'MIGRATION'
<?php
return new class extends \Illuminate\Database\Migrations\Migration {
    public function up(): void {
        if (\Illuminate\Support\Facades\Schema::hasTable('accounts')) {
            throw new \RuntimeException('Override ran after core.');
        }
        \Illuminate\Support\Facades\Schema::create('published_prerequisite', function ($table): void {
            $table->string('value');
        });
        \Illuminate\Support\Facades\DB::table('published_prerequisite')->insert(['value' => 'application override']);
    }
};
MIGRATION);
    $later = $databasePath.'/migrations/2026_11_19_000004_create_application_extension.php';
    file_put_contents($later, <<<'MIGRATION'
<?php
return new class extends \Illuminate\Database\Migrations\Migration {
    public function up(): void {
        if (! \Illuminate\Support\Facades\Schema::hasTable('accounts')) {
            throw new \RuntimeException('Application migration ran before core.');
        }
        \Illuminate\Support\Facades\Schema::create('application_extension', function ($table): void {
            $table->id();
        });
    }
};
MIGRATION);
    app()->useDatabasePath($databasePath);
    app(MigrationRegistry::class)->registerLaravel('test/prerequisite', __DIR__.'/../Fixtures/extension-migrations/before', beforeCore: true);
    config()->set('database.connections.override_test', ['driver' => 'sqlite', 'database' => $file, 'prefix' => '']);
    try {
        $this->artisan('gateway:migrate', ['--database' => 'override_test'])->assertSuccessful();
        $this->artisan('gateway:migrate', ['--database' => 'override_test'])->assertSuccessful();
        $pdo = new PDO('sqlite:'.$file);
        expect($pdo->query('SELECT value FROM published_prerequisite')->fetchAll(PDO::FETCH_COLUMN))->toBe(['application override'])
            ->and($pdo->query("SELECT name FROM sqlite_master WHERE name='adapter_prerequisites'")->fetchColumn())->toBeFalse()
            ->and($pdo->query("SELECT name FROM sqlite_master WHERE name='application_extension'")->fetchColumn())->toBe('application_extension')
            ->and((int) $pdo->query("SELECT COUNT(*) FROM migrations WHERE migration='2026_11_19_000001_create_adapter_prerequisite'")->fetchColumn())->toBe(1);
    } finally {
        app()->useDatabasePath($originalPath);
        app('db')->purge('override_test');
        unlink($override);
        unlink($later);
        rmdir($databasePath.'/migrations');
        rmdir($databasePath);
        unlink($file);
    }
});
