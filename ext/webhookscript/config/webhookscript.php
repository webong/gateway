<?php

declare(strict_types=1);

return [
    // Primi 0.4 matches Webhook.site's PHP/JS-like .ws syntax. PCNTL
    // provides a hard wall-clock deadline around each PHP execution.
    'max_source_bytes' => (int) env('GATEWAY_WEBHOOKSCRIPT_MAX_SOURCE_BYTES', 65536),
    'timeout_seconds' => (int) env('GATEWAY_WEBHOOKSCRIPT_TIMEOUT_SECONDS', 1),
    'max_deliveries' => (int) env('GATEWAY_WEBHOOKSCRIPT_MAX_DELIVERIES', 32),
    'max_logs' => (int) env('GATEWAY_WEBHOOKSCRIPT_MAX_LOGS', 100),
];
