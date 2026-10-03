<?php

declare(strict_types=1);

namespace Webong\Gateway\Tests\Fixtures;

use Illuminate\Support\Facades\DB;
use Webong\Gateway\Email\TxtResolver;

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
        $row = DB::table('email_domains')->where('domain', substr($name, strlen($prefix)))->first();

        return $row === null ? [] : ['gateway-verification='.$row->verification_token];
    }
}
