<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use RuntimeException;
use Webong\Gateway\Protocols\Delivery;

/** One bounded HTTPS request to a registered, public credential source. */
final readonly class HttpApplicationMailboxGrantSource implements ApplicationMailboxGrantSource
{
    public function __construct(private MailboxCredentialSourceStore $sources, private MailboxGrantHttpClient $http) {}

    public function resolve(string $sourceId, string $account, string $connection, string $protocol, string $address): array
    {
        $source = $this->sources->active($account, $sourceId);
        if ($source === null || ! MailboxCredentialSourceStore::safeUrl($source['url'])) {
            throw new RuntimeException('Mailbox credential source is unavailable.');
        }
        $body = json_encode(['account_id' => $account, 'connection_id' => $connection,
            'protocol' => $protocol, 'address' => $address], JSON_THROW_ON_ERROR);
        return $this->http->request($source, new Delivery($source['url'], method: 'POST',
            headers: ['Content-Type' => ['application/json']], body: $body));
    }
}
