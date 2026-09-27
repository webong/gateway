<?php

declare(strict_types=1);

namespace Webong\Gateway\WebhookScript;

use Webong\Gateway\Protocol\Delivery;
use Webong\Gateway\Protocol\Response;
use Webong\Gateway\Protocol\ScheduleCommand;

final readonly class WebhookScriptOutcome
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
