<?php

declare(strict_types=1);

return [
    // Exact "host/path" keys. Only enable retry when this source guarantees
    // idempotency for the same Idempotency-Key and body. Upload routes authorize
    // metadata only, and never replay. Both features are disabled by default.
    // 'routes' => ['gateway.example.test/events' => [
    //     'source' => 'https://producer.example.test/events',
    //     'upload' => true,
    //     'retry' => true,
    // ]],
    'routes' => [],
];
