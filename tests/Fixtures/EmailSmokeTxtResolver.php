<?php

declare(strict_types=1);

namespace Webong\Gateway\Tests\Fixtures;

use Webong\Gateway\Email\TxtResolver;
use Webong\Gateway\Resources\PlannerResourceStore;

/** Test-only DNS fixture for the local Email integration smoke. */
final class EmailSmokeTxtResolver implements TxtResolver
{
    public function records(string $name): array
    {
        if ($name === '_dmarc.quarantine.example.test') {
            return ['v=DMARC1; p=reject'];
        }
        if ($name === '_dmarc.example.test') {
            return ['v=DMARC1; p=none; sp=none; np=reject; psd=n'];
        }
        $prefix = '_gateway-verify.';
        if (! str_starts_with($name, $prefix)) {
            return [];
        }
        $row = app(PlannerResourceStore::class)->byKey('email.domain', substr($name, strlen($prefix)));

        return $row === null ? [] : ['gateway-verification='.$row->document['verification_token']];
    }
}
