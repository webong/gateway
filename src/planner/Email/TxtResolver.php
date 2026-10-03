<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

interface TxtResolver
{
    /** @return list<string> */
    public function records(string $name): array;
}
