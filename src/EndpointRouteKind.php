<?php

declare(strict_types=1);

namespace Webong\Gateway;

use InvalidArgumentException;

/** Planner-owned endpoint route selection stored in the existing metadata record. */
final class EndpointRouteKind
{
    public const DEFAULT = 'webhook';

    /** @param array<string, mixed> $metadata */
    public static function fromMetadata(array $metadata): string
    {
        $gateway = $metadata['_gateway'] ?? [];
        if (! is_array($gateway)) {
            throw new InvalidArgumentException('Endpoint Gateway metadata is malformed.');
        }
        $kind = $gateway['route_kind'] ?? self::DEFAULT;
        if (! is_string($kind) || preg_match('/\A[a-z][a-z0-9.-]{0,63}\z/D', $kind) !== 1) {
            throw new InvalidArgumentException('Endpoint route kind is malformed.');
        }
        return $kind;
    }

    /** @param array<string, mixed> $metadata @return array<string, mixed> */
    public static function withKind(array $metadata, string $kind): array
    {
        if (preg_match('/\A[a-z][a-z0-9.-]{0,63}\z/D', $kind) !== 1) {
            throw new InvalidArgumentException('Endpoint route kind is invalid.');
        }
        $gateway = $metadata['_gateway'] ?? [];
        if (! is_array($gateway)) {
            throw new InvalidArgumentException('Endpoint Gateway metadata is malformed.');
        }
        $metadata['_gateway'] = [...$gateway, 'route_kind' => $kind];
        return $metadata;
    }

    /** @param array<string, mixed> $metadata @return array<string, mixed> */
    public static function publicMetadata(array $metadata): array
    {
        $gateway = $metadata['_gateway'] ?? null;
        if (is_array($gateway)) {
            unset($gateway['route_kind']);
            if ($gateway === []) { unset($metadata['_gateway']); }
            else { $metadata['_gateway'] = $gateway; }
        }
        return $metadata;
    }
}
