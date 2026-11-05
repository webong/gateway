<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel\Console;

use Illuminate\Console\Command;
use Webong\Gateway\Migrations\GatewayMigrationRunner;

final class LaravelGatewayMigrateCommand extends Command
{
    protected $signature = 'gateway:migrate
        {--database= : Laravel database connection name}
        {--adopt-existing : Validate and record an existing Gateway schema without replaying it}';

    protected $description = 'Apply the framework-neutral Gateway schema migrations';

    public function handle(): int
    {
        $connectionName = $this->option('database') ?: config('database.default');
        $connection = config('database.connections.'.$connectionName);

        if (! is_array($connection)) {
            $this->components->error("Database connection [{$connectionName}] is not configured.");

            return self::FAILURE;
        }

        $tables = config('gateway.servers.tables', []);
        $runner = GatewayMigrationRunner::fromConnection(
            connection: $connection,
            migrationsPath: dirname(__DIR__, 3).'/migrations',
            tables: is_array($tables) ? $tables : [],
        );
        $result = $this->option('adopt-existing')
            ? $runner->adoptExistingSchema()
            : $runner->migrate();

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
}
