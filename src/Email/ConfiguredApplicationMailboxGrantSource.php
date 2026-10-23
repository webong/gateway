<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Illuminate\Contracts\Container\Container;
use RuntimeException;

/** Select an installed grant adapter by the account-owned source, never by a client-supplied class. */
final readonly class ConfiguredApplicationMailboxGrantSource implements ApplicationMailboxGrantSource
{
    public function __construct(private MailboxCredentialSourceStore $sources, private Container $container) {}

    public function resolve(string $sourceId, string $account, string $connection, string $protocol, string $address): array
    {
        try {
            $source = $this->sources->active($account, $sourceId);
            $adapters = config('gateway.email.credential_source_adapters', []);
            $class = is_array($adapters) && $source !== null
                ? ($adapters[$source['adapter']] ?? null) : null;
            if (! is_string($class) || $class === self::class) {
                throw new RuntimeException('Mailbox credential source is unavailable.');
            }
            $adapter = $this->container->make($class);
            if (! $adapter instanceof ApplicationMailboxGrantSource) {
                throw new RuntimeException('Mailbox credential source is unavailable.');
            }

            return $adapter->resolve($sourceId, $account, $connection, $protocol, $address);
        } catch (\Throwable) {
            // Client adapters may throw with remote response bodies or credentials.
            throw new RuntimeException('Mailbox credential source is unavailable.');
        }
    }
}
