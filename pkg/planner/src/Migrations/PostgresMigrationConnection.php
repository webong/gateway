<?php

declare(strict_types=1);

namespace Webong\Gateway\Migrations;

use InvalidArgumentException;
use PDO;

/** Opens neutral migration connections without losing PostgreSQL TLS policy. */
final class PostgresMigrationConnection
{
    /** @param array<string, mixed> $config */
    public static function connect(array $config): PDO
    {
        $values = [
            'host' => $config['host'] ?? '127.0.0.1',
            'port' => $config['port'] ?? 5432,
            'dbname' => $config['database'] ?? '',
        ];
        foreach (['sslmode', 'sslcert', 'sslkey', 'sslrootcert', 'connect_timeout', 'application_name'] as $option) {
            if (isset($config[$option])) {
                $values[$option] = $config[$option];
            }
        }
        $parts = [];
        foreach ($values as $name => $value) {
            // PDO separates DSN options on semicolons before libpq sees them.
            // Reject ambiguous values instead of allowing another DSN option.
            if (! is_scalar($value) || strpbrk((string) $value, ";\0") !== false) {
                throw new InvalidArgumentException("Invalid PostgreSQL connection option [{$name}].");
            }
            $parts[] = $name.'='.$value;
        }
        $pdo = new PDO(
            'pgsql:'.implode(';', $parts),
            $config['username'] ?? null,
            $config['password'] ?? null,
            array_replace([PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION], $config['options'] ?? []),
        );

        $searchPath = $config['search_path'] ?? $config['schema'] ?? null;
        if ($searchPath !== null) {
            $schemas = is_array($searchPath) ? $searchPath : str_getcsv((string) $searchPath, ',', '"', '');
            $quoted = array_map(static fn (string $schema): string => '"'.str_replace('"', '""', trim($schema)).'"', $schemas);
            if ($quoted === []) {
                throw new InvalidArgumentException('PostgreSQL search_path must not be empty.');
            }
            $pdo->exec('SET search_path TO '.implode(', ', $quoted));
        }

        return $pdo;
    }
}
