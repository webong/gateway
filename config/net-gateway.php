<?php

declare(strict_types=1);

return [
    // Required whenever the PHP worker can be reached outside the embedded
    // process.
    'internal_token' => (string) env('NET_GATEWAY_INTERNAL_TOKEN', ''),

    // Optional custom planner. If omitted, RegistryRoutePlanner is used when
    // path_resolver is configured.
    'planner' => env('NET_GATEWAY_PLANNER'),

    // The host Laravel application owns this resolver. It may use Laravel
    // routes, a database, or provider-specific path registration.
    'path_resolver' => env('NET_GATEWAY_PATH_RESOLVER'),

    // Bearer token accepted by the PHP-owned endpoint and subscription
    // registry API. Management endpoints fail closed until it is configured.
    'registry_token' => (string) env('REGISTRY_TOKEN', ''),

    'cache' => [
        // Use a shared store such as Redis in production so every warm PHP
        // worker observes generation-based invalidation immediately.
        'enabled' => (bool) env('NET_GATEWAY_CACHE_ENABLED', true),
        'store' => env('NET_GATEWAY_CACHE_STORE'),
        'prefix' => (string) env('NET_GATEWAY_CACHE_PREFIX', 'net-gateway'),

        // Path bindings opt in through CacheablePathResolver. Registry route
        // snapshots cache endpoint identity and database-backed destinations.
        'path_ttl' => (int) env('NET_GATEWAY_PATH_CACHE_TTL', 300),
        'route_ttl' => (int) env('NET_GATEWAY_ROUTE_CACHE_TTL', 30),
        'missing_ttl' => (int) env('NET_GATEWAY_MISSING_CACHE_TTL', 5),

        // Custom providers may select destinations from request payload or
        // headers, so they remain uncached unless the host explicitly opts in.
        'custom_providers' => (bool) env('NET_GATEWAY_CACHE_CUSTOM_PROVIDERS', false),
    ],

    'mutations' => [
        // Conditional subscription writes use a route-scoped atomic lock plus
        // If-Match revisions. Use the same shared Redis store as route caching
        // when multiple workers or application instances are running.
        'lock_store' => env('NET_GATEWAY_MUTATION_LOCK_STORE', env('NET_GATEWAY_CACHE_STORE')),
        'lock_seconds' => (int) env('NET_GATEWAY_MUTATION_LOCK_SECONDS', 10),
        'lock_wait_seconds' => (int) env('NET_GATEWAY_MUTATION_LOCK_WAIT_SECONDS', 5),
    ],
];
