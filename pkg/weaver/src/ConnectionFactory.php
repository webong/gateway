<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver;

use PDO;

final class ConnectionFactory
{
    /** Scoped secret value; the original PDO DSN preserves PostgreSQL TLS options. */
    public static function fromSecret(array $secret): PDO
    {
        return self::connect($secret,false,true);
    }

    /** Offline copy sources must not create files or change SQLite journal mode. */
    public static function readOnlySource(array $secret): PDO
    {
        return self::connect($secret,true,false);
    }

    /** Validation must precede journal/schema changes, even for an alias of source. */
    public static function cutoverDestination(array $secret): PDO
    {
        return self::connect($secret,false,false);
    }

    private static function connect(array $secret,bool $readOnly,bool $journal): PDO
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
            $options=[PDO::ATTR_ERRMODE=>PDO::ERRMODE_EXCEPTION,PDO::ATTR_EMULATE_PREPARES=>false];
            if($readOnly && str_starts_with($secret['dsn'],'sqlite:')){$options[PDO::SQLITE_ATTR_OPEN_FLAGS]=PDO::SQLITE_OPEN_READONLY;}
            $connection=new PDO($secret['dsn'],$secret['username'] ?? null,$secret['password'] ?? null,$options);
            if (OperationalSchema::driver($connection)==='sqlite') {
                $connection->exec('PRAGMA busy_timeout = 5000');
                if($journal){$connection->exec('PRAGMA journal_mode = WAL');}
                if($readOnly){$connection->exec('PRAGMA query_only = ON');}
            }
            if($readOnly && OperationalSchema::driver($connection)==='postgres'){$connection->exec('SET SESSION CHARACTERISTICS AS TRANSACTION READ ONLY');}
            if($readOnly && OperationalSchema::driver($connection)==='mysql'){$connection->exec('SET SESSION TRANSACTION READ ONLY');}
            return $connection;
        } catch (\PDOException) {
            // Secrets/DSNs do not enter logs through a chained exception.
            throw new \RuntimeException('WorkFlow operational connection is unavailable.');
        }
    }
}
