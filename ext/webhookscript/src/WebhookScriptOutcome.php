<?php

declare(strict_types=1);

namespace Webong\Gateway\WebhookScript;

use Webong\Gateway\Protocol\Delivery;
use Webong\Gateway\Protocol\Response;

final readonly class WebhookScriptOutcome
{
    /**
     * @param list<Delivery> $deliveries
     * @param array<string, mixed> $variables
     * @param list<string> $logs
     */
    public function __construct(
        public ?Response $response,
        public array $deliveries,
        public array $variables,
        public array $logs,
    ) {
    }
}
