<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

/** Resolve one transient transport grant; callers validate identity, capability and expiry before use.
 *  Adapters may read registered source details but must never persist provider access tokens.
 */
interface ApplicationMailboxGrantSource
{
    /** @return array<string, mixed> */
    public function resolve(string $sourceId, string $account, string $connection, string $protocol, string $address): array;
}
