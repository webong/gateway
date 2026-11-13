<?php

declare(strict_types=1);

/**
 * Report which database the bundled Planner application resolves under the
 * current environment, then exit. Booted exactly like migrations, seeding and
 * the host, so inherited DB_URL / DB_* / APP_*_CACHE settings take the same
 * effect they would there. Performs reads only and prints one JSON object.
 *
 * Used before the first write to prove a disposable run cannot be redirected to
 * another database. For SQLite the opened file comes from `PRAGMA database_list`;
 * for PostgreSQL the report carries the database, the endpoint Laravel was given,
 * and the server identity it actually reached.
 */

$root = dirname(__DIR__, 2);

require $root.'/vendor/autoload.php';

$app = require $root.'/app/planner/bootstrap/app.php';
$app->make(Illuminate\Contracts\Console\Kernel::class)->bootstrap();

$connection = $app->make('db')->connection();
$config = $connection->getConfig();

$report = [
    'default' => $app['config']->get('database.default'),
    'driver' => $connection->getDriverName(),
    'database' => $config['database'] ?? null,
    'host' => $config['host'] ?? null,
    'port' => isset($config['port']) ? (int) $config['port'] : null,
    'config_cached' => $app->configurationIsCached(),
    'config_cache_path' => $app->getCachedConfigPath(),
];

if ($report['driver'] === 'sqlite') {
    $report['file'] = $connection->select('PRAGMA database_list')[0]->file ?? null;
} elseif ($report['driver'] === 'pgsql') {
    $row = $connection->selectOne(
        'SELECT current_database() AS database, inet_server_port() AS port, pg_postmaster_start_time()::text AS started',
    );
    $report['identity'] = [
        'database' => (string) $row->database,
        'port' => $row->port === null ? null : (int) $row->port,
        'started' => (string) $row->started,
    ];
}

echo json_encode($report, JSON_THROW_ON_ERROR), PHP_EOL;
