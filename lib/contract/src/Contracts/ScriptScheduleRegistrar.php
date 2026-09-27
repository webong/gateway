<?php

declare(strict_types=1);

namespace Webong\Gateway\Contracts;

use Webong\Gateway\Protocol\ScheduleCommand;

interface ScriptScheduleRegistrar
{
    public function register(string $origin, ScheduleCommand ...$schedules): void;
}
