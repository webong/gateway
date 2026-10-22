<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use RuntimeException;

/** Lets account-owned scripts shape a credential request without handling its bearer secret. */
final readonly class ScriptedApplicationMailboxGrantSource implements ApplicationMailboxGrantSource
{
    public function __construct(private MailboxCredentialSourceStore $sources,
        private CredentialScriptRegistry $scripts, private MailboxGrantHttpClient $http) {}

    public function resolve(string $sourceId, string $account, string $connection, string $protocol, string $address): array
    {
        $source = $this->sources->active($account, $sourceId);
        if ($source === null || ! is_string($source['language'] ?? null) || ! is_string($source['source'] ?? null)) {
            throw new RuntimeException('Mailbox credential source is unavailable.');
        }
        $delivery = $this->scripts->execute($source['language'], $source['source'], [
            'account_id' => $account,
            'connection_id' => $connection,
            'protocol' => $protocol,
            'address' => $address,
            'source_url' => $source['url'],
        ]);

        return $this->http->request($source, $delivery);
    }
}
