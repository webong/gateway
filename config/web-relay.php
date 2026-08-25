<?php

declare(strict_types=1);

return [
    // Required whenever the PHP worker can be reached outside the embedded
    // process. It must match ROADRUNNER_INTERNAL_TOKEN in the Go service.
    'internal_token' => (string) env('ROADRUNNER_INTERNAL_TOKEN', ''),

    // Optional custom planner. If omitted, RegistryRoutePlanner is used when
    // path_resolver is configured.
    'planner' => env('WEB_RELAY_PLANNER'),

    // The host Laravel application owns this resolver. It may use Laravel
    // routes, a database, or provider-specific path registration.
    'path_resolver' => env('WEB_RELAY_PATH_RESOLVER'),

    // Bearer token accepted by the PHP-owned endpoint and subscription
    // registry API. Management endpoints fail closed until it is configured.
    'registry_token' => (string) env('REGISTRY_TOKEN', ''),
];
