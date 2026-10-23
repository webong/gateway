<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use RuntimeException;
use Throwable;

final class NativeMxResolver implements MxResolver
{
    public function records(string $domain): array
    {
        try { $records = @dns_get_record($domain, DNS_MX); }
        catch (Throwable $exception) { throw new RuntimeException('MX lookup is unavailable.', previous: $exception); }
        if (! is_array($records)) { throw new RuntimeException('MX lookup is unavailable.'); }
        $answer = [];
        foreach ($records as $record) {
            if (is_string($record['target'] ?? null) && is_int($record['pri'] ?? null)) {
                $answer[] = ['host' => strtolower(rtrim($record['target'], '.')), 'priority' => $record['pri']];
            }
        }
        return $answer;
    }
}
