<?php

declare(strict_types=1);

return [
    // Injected into every managed Reverb process by the Gateway runtime.
    // The application provider fails closed when no server identity exists.
    'server_id' => env('GATEWAY_REVERB_SERVER_ID'),
];
