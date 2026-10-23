<?php

declare(strict_types=1);

namespace Webong\Gateway\HookScript;

use Webong\Gateway\Protocols\Delivery;
use Webong\Gateway\Protocols\Response;
use Webong\Gateway\Protocols\ScheduleCommand;

final readonly class HookScriptOutcome
{
    /**
     * @param list<Delivery> $deliveries
     * @param list<ScheduleCommand> $schedules
     * @param array<string, mixed> $variables
     * @param list<string> $logs
     */
    public function __construct(
        public ?Response $response,
        public array $deliveries,
        public array $schedules,
        public array $variables,
        public array $logs,
    ) {
    }
}
