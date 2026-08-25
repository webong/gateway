<?php

declare(strict_types=1);

namespace Webong\WebRelay\Protocol;

use InvalidArgumentException;

final class SubscriptionState
{
    public const string STATUS_METADATA_KEY = '_web_relay_status';

    public const string REVISION_METADATA_KEY = '_web_relay_revision';

    public const string ACTIVE = 'active';

    public const string PAUSED = 'paused';

    public const string REMOVED = 'removed';

    /** @param array<string, mixed> $metadata */
    public static function status(array $metadata): string
    {
        $status = $metadata[self::STATUS_METADATA_KEY] ?? self::ACTIVE;

        return is_string($status) && in_array($status, self::statuses(), true)
            ? $status
            : self::REMOVED;
    }

    /** @param array<string, mixed> $metadata */
    public static function revision(array $metadata): string
    {
        $revision = $metadata[self::REVISION_METADATA_KEY] ?? null;

        return is_string($revision) && preg_match('/^[a-f0-9]{32}$/', $revision) === 1
            ? $revision
            : 'legacy';
    }

    public static function newRevision(): string
    {
        return bin2hex(random_bytes(16));
    }

    public static function etag(string $revision): string
    {
        return '"'.$revision.'"';
    }

    public static function parseEtag(?string $etag): string
    {
        if ($etag === null || trim($etag) === '') {
            throw new InvalidArgumentException('An If-Match subscription revision is required.');
        }

        $etag = trim($etag);
        if (preg_match('/^"([a-f0-9]{32}|legacy)"$/', $etag, $matches) !== 1) {
            throw new InvalidArgumentException('If-Match must contain the quoted subscription revision.');
        }

        return $matches[1];
    }

    /** @return list<string> */
    public static function statuses(): array
    {
        return [self::ACTIVE, self::PAUSED, self::REMOVED];
    }
}
