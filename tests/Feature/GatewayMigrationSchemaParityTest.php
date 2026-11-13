<?php

declare(strict_types=1);

use Illuminate\Database\Capsule\Manager as Capsule;
use Illuminate\Support\Facades\DB;
use Webong\Gateway\Migrations\GatewayMigrationRunner;

it('matches every Laravel-managed Planner table and column', function (): void {
    $database = tempnam(sys_get_temp_dir(), 'gateway-schema-parity-');
    $tables = config('gateway.servers.tables');
    $logicalTables = [
        'accounts',
        'targets',
        'servers',
        'applications',
        'instances',
        'nodes',
        'events',
        'schedules',
        'schedule_runs',
        'schedule_notifications',
        'usage_counters',
        'deliveries',
        'gateway_cache',
        'gateway_cache_locks',
        'delivery_admin_endpoints',
        'configurations',
        'configuration_revisions',
        'resource_observations',
        'configuration_secrets',
        'resource_checkpoints',
        'companion_action_executions',
    ];

    try {
        GatewayMigrationRunner::sqlite(
            database: $database,
            migrationsPath: dirname(__DIR__, 2).'/pkg/planner/migrations',
            tables: $tables,
        )->migrate();

        $neutral = new Capsule();
        $neutral->addConnection([
            'driver' => 'sqlite',
            'database' => $database,
            'foreign_key_constraints' => true,
        ], 'neutral');
        $neutralSchema = $neutral->getConnection('neutral')->getSchemaBuilder();
        $laravelSchema = DB::connection()->getSchemaBuilder();

        foreach ($logicalTables as $logicalTable) {
            $table = $tables[$logicalTable] ?? $logicalTable;
            $legacyColumns = $laravelSchema->getColumnListing($table);
            $neutralColumns = $neutralSchema->getColumnListing($table);
            sort($legacyColumns);
            sort($neutralColumns);

            expect($neutralColumns)->toBe($legacyColumns, "Schema differs for {$table}");
        }
    } finally {
        if (is_string($database) && file_exists($database)) {
            unlink($database);
        }
    }
});
