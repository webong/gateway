<?php

declare(strict_types=1);

namespace Webong\Gateway\ZorviaMailbox;

use RuntimeException;
use Webong\Gateway\Email\ApplicationMailboxGrantSource;
use Webong\Gateway\Email\MailboxCredentialSourceStore;

/** Translate Gateway's neutral resolution request into Zorvia's setup contract. */
final readonly class ZorviaApplicationMailboxGrantSource implements ApplicationMailboxGrantSource
{
    public const ADAPTER = 'zorvia';

    public function __construct(
        private MailboxCredentialSourceStore $sources,
        private ZorviaMailboxGrantTransport $transport,
    ) {}

    public function resolve(string $sourceId, string $account, string $connection, string $protocol, string $address): array
    {
        try {
            $source = $this->sources->active($account, $sourceId);
            $method = config('gateway-zorvia-mailbox.method', 'PATCH');
            if ($source === null || $source['adapter'] !== self::ADAPTER ||
                ! in_array($method, ['PATCH', 'PUT'], true)) {
                throw new RuntimeException('Mailbox credential source is unavailable.');
            }
            $context = [
                'account_id' => $account, 'connection_id' => $connection,
                'protocol' => $protocol, 'address' => $address,
            ];
            $response = $this->transport->exchange($source['url'], $method, [
                'Authorization: Bearer '.$source['authorization'],
                'Content-Type: application/json', 'Accept: application/json',
            ], json_encode(['context' => $context], JSON_THROW_ON_ERROR));
            if (($response['status'] ?? null) !== 200 || ! is_string($response['body'] ?? null) ||
                strlen($response['body']) > 16384) {
                throw new RuntimeException('Mailbox credential source is unavailable.');
            }
            $grant = json_decode($response['body'], true, 16, JSON_THROW_ON_ERROR);
            if (! is_array($grant)) { throw new RuntimeException('Mailbox credential source is unavailable.'); }

            return $grant;
        } catch (\Throwable) {
            // Do not expose the source bearer or a remote response body.
            throw new RuntimeException('Mailbox credential source is unavailable.');
        }
    }
}
