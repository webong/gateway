<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel\Console;

use Illuminate\Console\Command;
use Webong\Gateway\Migrations\GatewayMigrationRunner;
use Webong\Gateway\Migrations\MigrationRegistry;

final class LaravelGatewayMigrateCommand extends Command
{
    protected $signature = 'gateway:migrate
        {--database= : Laravel database connection name}
        {--force : Run Laravel migrations without a production confirmation}
        {--adopt-existing : Validate and record an existing Gateway schema without replaying it}';

    protected $description = 'Apply Gateway, adapter and registered extension schema migrations';

    public function handle(): int
    {
        $connectionName = $this->option('database') ?: config('database.default');

        if (! is_array(config('database.connections.'.$connectionName))) {
            $this->components->error("Database connection [{$connectionName}] is not configured.");

            return self::FAILURE;
        }

        // Use the configuration Laravel resolves for the connection, not the raw
        // array: a `url` (DB_URL / DATABASE_URL) only takes effect once Laravel
        // has parsed it, and the standalone Planner is configured with DB_URL
        // alone. Reuse its configured write PDO for every supported driver so
        // TLS options, sockets and session settings agree with the application.
        $database = $this->laravel['db']->connection($connectionName);
        $connection = $database->getConfig();

        $migrations = $this->laravel->make(MigrationRegistry::class);
        $tables = config('gateway.servers.tables', []);
        $runner = GatewayMigrationRunner::fromConnection(
            connection: $connection,
            migrationsPath: $migrations,
            tables: is_array($tables) ? $tables : [],
            pdo: $database->getPdo(),
        );
        $beforeCore = $migrations->laravelPaths(true);
        // Resolve matching application overrides before prerequisites are logged,
        // without pulling unrelated application migrations ahead of core.
        $migrator = $this->laravel->make('migrator');
        $prerequisites = $migrator->getMigrationFiles($beforeCore);
        $resolved = $migrator->getMigrationFiles([...$beforeCore, database_path('migrations')]);
        $prerequisiteFiles = array_values(array_intersect_key($resolved, $prerequisites));
        if ($prerequisiteFiles !== [] && $this->migrateLaravel($connectionName, $prerequisiteFiles) !== self::SUCCESS) {
            return self::FAILURE;
        }

        $result = $this->option('adopt-existing')
            ? $runner->adoptExistingSchema()
            : $runner->migrate();

        // Native registrations (loadMigrationsFrom) and application migrations
        // retain Laravel's existing ledger and discovery/filename semantics.
        $paths = [...$this->laravel->make('migrator')->paths(), ...$migrations->laravelPaths(false), database_path('migrations')];
        $canonical = static fn (string $path): string => realpath($path) ?: $path;
        $paths = array_values(array_diff(array_unique(array_map($canonical, $paths)), array_map($canonical, $beforeCore)));
        $paths = array_values(array_filter($paths, 'is_dir'));
        if ($paths !== [] && $this->migrateLaravel($connectionName, $paths) !== self::SUCCESS) {
            return self::FAILURE;
        }

        if ($result->applied === []) {
            $this->components->info('Gateway schema is already current.');

            return self::SUCCESS;
        }

        $this->components->info(sprintf(
            'Applied %d Gateway migration%s.',
            count($result->applied),
            count($result->applied) === 1 ? '' : 's',
        ));

        return self::SUCCESS;
    }

    /** @param list<string> $paths */
    private function migrateLaravel(string $connectionName, array $paths): int
    {
        return $this->call('migrate', [
            '--database' => $connectionName,
            '--path' => $paths,
            '--realpath' => true,
            '--force' => (bool) $this->option('force'),
        ]);
    }
}
