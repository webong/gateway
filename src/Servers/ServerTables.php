<?php

declare(strict_types=1);

namespace Webong\Gateway\Servers;

final class ServerTables
{
    public static function servers(): string
    {
        return self::configured('servers', 'servers');
    }

    public static function applications(): string
    {
        return self::configured('applications', 'applications');
    }

    public static function instances(): string
    {
        return self::configured('instances', 'instances');
    }

    public static function nodes(): string
    {
        return self::configured('nodes', 'gateway_nodes');
    }

    private static function configured(string $name, string $default): string
    {
        $table = config("gateway.servers.tables.{$name}");

        return is_string($table) && trim($table) !== '' ? $table : $default;
    }
}
