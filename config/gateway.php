<?php

declare(strict_types=1);

return [
    // Required whenever the PHP worker can be reached outside the embedded
    // process.
    'internal_token' => (string) env('GATEWAY_INTERNAL_TOKEN', ''),

    // Optional custom planner. If omitted, RegistryRoutePlanner is used when
    // path_resolver is configured.
    'planner' => env('GATEWAY_PLANNER'),

    // Optional planner for protocol-neutral events such as SMTP and
    // WebSocket. When omitted, the registry route planner is adapted when a
    // path resolver is configured.
    'protocol_planner' => env('GATEWAY_PROTOCOL_PLANNER'),

    // The host Laravel application owns this resolver. It may use Laravel
    // routes, a database, or provider-specific path registration.
    'path_resolver' => env('GATEWAY_PATH_RESOLVER'),

    // Bearer token accepted by the PHP-owned endpoint and subscription
    // registry API. Management endpoints fail closed until it is configured.
    'registry_token' => (string) env('REGISTRY_TOKEN', ''),

    // Gateway core owns the managed-server control-plane records. Configure
    // table names before running the package migrations.
    'servers' => [
        'tables' => [
            'servers' => env('GATEWAY_SERVERS_TABLE', 'servers'),
            'applications' => env('GATEWAY_APPLICATIONS_TABLE', 'applications'),
            'instances' => env('GATEWAY_INSTANCES_TABLE', 'instances'),
        ],
    ],

    'cache' => [
        // Use a shared store such as Redis in production so every warm PHP
        // worker observes generation-based invalidation immediately.
        'enabled' => (bool) env('GATEWAY_CACHE_ENABLED', true),
        'store' => env('GATEWAY_CACHE_STORE'),
        'prefix' => (string) env('GATEWAY_CACHE_PREFIX', 'gateway'),

        // Path bindings opt in through CacheablePathResolver. Registry route
        // snapshots cache endpoint identity and database-backed destinations.
        'path_ttl' => (int) env('GATEWAY_PATH_CACHE_TTL', 300),
        'route_ttl' => (int) env('GATEWAY_ROUTE_CACHE_TTL', 30),
        'missing_ttl' => (int) env('GATEWAY_MISSING_CACHE_TTL', 5),

        // Custom providers may select destinations from request payload or
        // headers, so they remain uncached unless the host explicitly opts in.
        'custom_providers' => (bool) env('GATEWAY_CACHE_CUSTOM_PROVIDERS', false),
    ],

    'mutations' => [
        // Conditional subscription writes use a route-scoped atomic lock plus
        // If-Match revisions. Use the same shared Redis store as route caching
        // when multiple workers or application instances are running.
        'lock_store' => env('GATEWAY_MUTATION_LOCK_STORE', env('GATEWAY_CACHE_STORE')),
        'lock_seconds' => (int) env('GATEWAY_MUTATION_LOCK_SECONDS', 10),
        'lock_wait_seconds' => (int) env('GATEWAY_MUTATION_LOCK_WAIT_SECONDS', 5),
    ],
];
