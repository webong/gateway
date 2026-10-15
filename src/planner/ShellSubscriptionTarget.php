<?php

declare(strict_types=1);

namespace Webong\Gateway;

use InvalidArgumentException;

/** Keeps Proxy's URL-shaped storage detail out of Shell subscriptions. */
final class ShellSubscriptionTarget
{
    private const PREFIX = 'gateway-shell://local/';

    public static function encode(string $hook): string
    {
        self::assertHook($hook);

        return self::PREFIX.implode('/', array_map(rawurlencode(...), explode('/', $hook)));
    }

    public static function isShell(string $target): bool
    {
        return str_starts_with($target, 'gateway-shell:');
    }

    public static function hook(string $target): string
    {
        if (! str_starts_with($target, self::PREFIX)) {
            throw new InvalidArgumentException('The subscription contains a malformed Shell target.');
        }

        $encoded = substr($target, strlen(self::PREFIX));
        $hook = implode('/', array_map(rawurldecode(...), explode('/', $encoded)));
        self::assertHook($hook);
        if (! hash_equals(self::encode($hook), $target)) {
            throw new InvalidArgumentException('The subscription contains a malformed Shell target.');
        }

        return $hook;
    }

    public static function assertHook(string $hook): void
    {
        if (strlen($hook) > 1024
            || preg_match('~^[A-Za-z0-9_-][A-Za-z0-9._-]*(?:/[A-Za-z0-9_-][A-Za-z0-9._-]*)*$~D', $hook) !== 1) {
            throw new InvalidArgumentException('Shell hook must be a non-hidden relative path.');
        }
    }
}
