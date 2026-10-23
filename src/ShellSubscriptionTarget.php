<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Illuminate\Support\Str;
use InvalidArgumentException;

/** Keeps Proxy's URL-shaped storage detail out of Shell subscriptions. */
final class ShellSubscriptionTarget
{
    private const PREFIX = 'gateway-shell://local/';
    private const SSH_PREFIX = 'gateway-shell://ssh/';

    public static function encode(string $hook): string
    {
        self::assertHook($hook);

        return self::PREFIX.implode('/', array_map(rawurlencode(...), explode('/', $hook)));
    }

    public static function isShell(string $target): bool
    {
        return str_starts_with($target, 'gateway-shell:');
    }

    public static function encodeSsh(string $id, string $hook): string
    {
        if (! Str::isUuid($id)) {
            throw new InvalidArgumentException('SSH execution reference is invalid.');
        }
        self::assertHook($hook);

        return self::SSH_PREFIX.$id.'/'.implode('/', array_map(rawurlencode(...), explode('/', $hook)));
    }

    public static function sshId(string $target): ?string
    {
        if (! str_starts_with($target, self::SSH_PREFIX)) {
            return null;
        }
        $parts = explode('/', substr($target, strlen(self::SSH_PREFIX)), 2);
        if (count($parts) !== 2 || ! Str::isUuid($parts[0])) {
            throw new InvalidArgumentException('The subscription contains a malformed SSH Shell target.');
        }
        $hook = implode('/', array_map(rawurldecode(...), explode('/', $parts[1])));
        if (! hash_equals(self::encodeSsh($parts[0], $hook), $target)) {
            throw new InvalidArgumentException('The subscription contains a malformed SSH Shell target.');
        }

        return $parts[0];
    }

    public static function hook(string $target): string
    {
        if (($id = self::sshId($target)) !== null) {
            $encoded = substr($target, strlen(self::SSH_PREFIX.$id.'/'));

            return implode('/', array_map(rawurldecode(...), explode('/', $encoded)));
        }
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

    public static function assertMetadataKey(string $key): void
    {
        if (in_array(strtolower($key), ['private_key', 'password', 'credential', 'credentials', 'ssh'], true)) {
            throw new InvalidArgumentException('Shell credential material cannot be stored in subscription metadata.');
        }
    }
}
