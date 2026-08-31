<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

final class DnsHookTables
{
    public static function hooks(): string
    {
        return self::configured('hooks', 'dns_hooks');
    }

    public static function events(): string
    {
        return self::configured('events', 'dns_hook_events');
    }

    private static function configured(string $name, string $default): string
    {
        $table = config("gateway.dns.tables.{$name}");

        return is_string($table) && trim($table) !== '' ? $table : $default;
    }
}
