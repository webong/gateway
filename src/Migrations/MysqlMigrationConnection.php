<?php

declare(strict_types=1);

namespace Webong\Gateway\Migrations;

use InvalidArgumentException;
use PDO;

/** Preserves PDO MySQL options, including TLS, for neutral migration hosts. */
final class MysqlMigrationConnection
{
    /** @param array<string, mixed> $config */
    public static function connect(array $config): PDO
    {
        $values = ! empty($config['unix_socket'])
            ? ['unix_socket' => $config['unix_socket']]
            : ['host' => $config['host'] ?? '127.0.0.1', 'port' => $config['port'] ?? 3306];
        $values['dbname'] = $config['database'] ?? '';
        $values['charset'] = $config['charset'] ?? 'utf8mb4';
        $parts = [];
        foreach ($values as $name => $value) {
            if (! is_scalar($value) || strpbrk((string) $value, ";\0") !== false) {
                throw new InvalidArgumentException("Invalid MySQL connection option [{$name}].");
            }
            $parts[] = $name.'='.$value;
        }

        return new PDO(
            'mysql:'.implode(';', $parts),
            $config['username'] ?? null,
            $config['password'] ?? null,
            array_replace([PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION], $config['options'] ?? []),
        );
    }
}
