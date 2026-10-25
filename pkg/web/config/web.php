<?php

declare(strict_types=1);

return [
    'max_body_bytes' => (int) env('GATEWAY_EVENTS_MAX_BODY_BYTES', 65536),
];
