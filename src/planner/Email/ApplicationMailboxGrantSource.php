<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

/** Resolve one transient, account- and connection-scoped transport grant. */
interface ApplicationMailboxGrantSource
{
    /** @return array<string, mixed> */
    public function resolve(string $sourceId, string $account, string $connection, string $protocol, string $address): array;
}
