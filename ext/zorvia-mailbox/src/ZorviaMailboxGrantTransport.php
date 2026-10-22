<?php

declare(strict_types=1);

namespace Webong\Gateway\ZorviaMailbox;

interface ZorviaMailboxGrantTransport
{
    /**
     * @param list<string> $headers
     * @return array{status: int, body: string}
     */
    public function exchange(string $url, string $method, array $headers, string $body): array;
}
