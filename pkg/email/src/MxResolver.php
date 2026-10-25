<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

interface MxResolver
{
    /** @return list<array{host: string, priority: int}> */
    public function records(string $domain): array;
}
