<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver;

use PDO;

final class ConnectionFactory
{
    /** Scoped secret value; the original PDO DSN preserves PostgreSQL TLS options. */
    public static function fromSecret(array $secret): PDO
    {
        if (array_diff(array_keys($secret), ['dsn','username','password']) !== []
            || !is_string($secret['dsn'] ?? null)
            || !preg_match('/\A(?:sqlite:|pgsql:|mysql:)/D',$secret['dsn'])
            || str_contains($secret['dsn'],"\0")) {
            throw new \InvalidArgumentException('Invalid WorkFlow database secret.');
        }
        foreach (['username','password'] as $key) {
            if (isset($secret[$key]) && !is_string($secret[$key])) {
                throw new \InvalidArgumentException('Invalid WorkFlow database secret.');
            }
        }
        try {
            $connection=new PDO($secret['dsn'],$secret['username'] ?? null,$secret['password'] ?? null,
                [PDO::ATTR_ERRMODE=>PDO::ERRMODE_EXCEPTION,PDO::ATTR_EMULATE_PREPARES=>false]);
            if (OperationalSchema::driver($connection)==='sqlite') {
                $connection->exec('PRAGMA busy_timeout = 5000');
                $connection->exec('PRAGMA journal_mode = WAL');
            }
            return $connection;
        } catch (\PDOException) {
            // Secrets/DSNs do not enter logs through a chained exception.
            throw new \RuntimeException('WorkFlow operational connection is unavailable.');
        }
    }
}
