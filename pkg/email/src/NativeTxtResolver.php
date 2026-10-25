<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use RuntimeException;
use Throwable;

final class NativeTxtResolver implements TxtResolver
{
    public function records(string $name): array
    {
        try {
            $records = @dns_get_record($name, DNS_TXT);
        } catch (Throwable $exception) {
            throw new RuntimeException('DNS verification lookup is unavailable.', previous: $exception);
        }
        if (! is_array($records)) {
            throw new RuntimeException('DNS verification lookup is unavailable.');
        }

        return array_values(array_filter(array_map(
            static fn (array $record): string => is_array($record['entries'] ?? null)
                ? implode('', $record['entries'])
                : (string) ($record['txt'] ?? ''),
            $records,
        ), static fn (string $value): bool => $value !== ''));
    }
}
