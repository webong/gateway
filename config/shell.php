<?php

declare(strict_types=1);

return [
    // Separate operator approval for managing privileged hook targets.
    'subscription_token' => (string) env('GATEWAY_SHELL_SUBSCRIPTION_TOKEN', ''),
];
