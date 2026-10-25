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

    // Private Spinner delivery-admin origin. Planner authenticates callers
    // with REGISTRY_TOKEN and calls this origin using GATEWAY_INTERNAL_TOKEN.
    'delivery_admin_url' => (string) env('GATEWAY_DELIVERY_ADMIN_URL', ''),

    // Ordered private admin origins backed by the same PostgreSQL delivery
    // queue. This is distinct from independent local-spool nodes below.
    'delivery_admin_shared_urls' => env('GATEWAY_DELIVERY_ADMIN_SHARED_URLS'),
    'delivery_admin_discovery_enabled' => (bool) env('GATEWAY_DELIVERY_ADMIN_DISCOVERY_ENABLED', false),

    // Optional JSON object of stable node IDs to private admin origins for
    // deployments with a separate local spool on each Spinner node.
    'delivery_admin_nodes' => env('GATEWAY_DELIVERY_ADMIN_NODES'),

    'schedule_notifications' => [
        // Exact approved receiver hosts; HTTP is accepted only for 127.0.0.1.
        'allowed_hosts' => (string) env('GATEWAY_SCHEDULE_NOTIFICATION_ALLOWED_HOSTS', ''),
    ],

    'automation' => [
        // Optional fallback for third-party Spinner extensions without a PHP
        // companion. Built-in extensions register through their providers,
        // and Spinner still has to advertise each engine at runtime.
        'languages' => array_values(array_filter(array_map(
            static fn (string $language): string => trim($language),
            explode(',', (string) env('GATEWAY_AUTOMATION_LANGUAGES', '')),
        ))),
    ],

    'companion_actions' => [
        // Deployment-owned ext package store. Which installed extension is
        // enabled, and whether its static or dynamic companion is selected,
        // remains Planner CMS state in the generic configurations table.
        'plugin_dir' => (string) env('GATEWAY_COMPANION_PLUGIN_DIR', ''),
        'generation_root' => (string) env('GATEWAY_COMPANION_GENERATION_ROOT', storage_path('framework/cache/gateway/companion-generations')),
        // PHP-FPM reports its own executable through PHP_BINARY. Point this at
        // the PHP CLI binary when Planner does not run under CLI/Octane.
        'php_binary' => (string) env('GATEWAY_COMPANION_PHP_BINARY', PHP_BINARY),
        // Deployment safety ceilings. Planner accepts typed invocation data;
        // dynamic PHP is launched only from verified ext package artifacts.
        'max_input_bytes' => 262144,
        'max_runtime_seconds' => 300,
        'max_output_bytes' => 1048576,
        // Idempotency is guaranteed inside this operational retention window.
        'retention_days' => 30,
    ],

    'shell' => [
        // Separate operator approval for managing privileged local hooks.
        'subscription_token' => (string) env('GATEWAY_SHELL_SUBSCRIPTION_TOKEN', ''),
    ],

    // Gateway core owns the managed-server control-plane records. Configure
    // table names before running the package migrations.
    'servers' => [
        'tables' => [
            'servers' => env('GATEWAY_SERVERS_TABLE', 'servers'),
            'applications' => env('GATEWAY_APPLICATIONS_TABLE', 'applications'),
            'instances' => env('GATEWAY_INSTANCES_TABLE', 'instances'),
            'nodes' => env('GATEWAY_NODES_TABLE', 'nodes'),
            'targets' => env('GATEWAY_TARGETS_TABLE', 'targets'),
        ],
        // Nodes are considered unavailable after this interval without an
        // assignment heartbeat. All reconciler nodes must use the same value.
        'coordination_lease_seconds' => (int) env('GATEWAY_PROVISIONING_COORDINATION_LEASE_SECONDS', 15),
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
