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
        // Opt-in operator webhook for failed schedule executions or deliveries.
        // All Planner workers should share this configuration.
        'url' => (string) env('GATEWAY_SCHEDULE_NOTIFICATION_URL', ''),
        'token' => (string) env('GATEWAY_SCHEDULE_NOTIFICATION_TOKEN', ''),
    ],

    'email' => [
        // Refresh TXT ownership during SMTP planning after this interval.
        // Zero checks every transaction; a stale, unverifiable domain fails
        // temporarily instead of accepting mail on historical ownership.
        'verification_ttl_seconds' => (int) env('GATEWAY_EMAIL_VERIFICATION_TTL_SECONDS', 86400),
        // Public forwarding is not enabled by this switch: it only applies a
        // sender's DMARC p=reject request after conclusive failed alignment.
        'dmarc_enforce_enabled' => (bool) env('GATEWAY_EMAIL_DMARC_ENFORCE_ENABLED', false),
        // Optional deployment-owned scanner/abuse policy class. It is
        // resolved by Planner, never selected by an SMTP client or domain.
        'policy' => env('GATEWAY_EMAIL_POLICY'),
        // Optional deployment-owned SMTP AUTH authority. It verifies the
        // credential and returns exact sender grants; registry routes never
        // receive AUTH passwords.
        'submission_authenticator' => env('GATEWAY_EMAIL_SUBMISSION_AUTHENTICATOR'),
        // Public HTTP submission is a separate opt-in from SMTP AUTH.
        'http_submission_enabled' => (bool) env('GATEWAY_EMAIL_HTTP_SUBMISSION_ENABLED', false),
        // Zero disables the shared UTC-day submission-attempt quota unless an
        // authenticator returns an account-specific limit in its grant.
        'submission_daily_limit' => (int) env('GATEWAY_EMAIL_SUBMISSION_DAILY_LIMIT', 0),
        'submission_daily_byte_limit' => (int) env('GATEWAY_EMAIL_SUBMISSION_DAILY_BYTE_LIMIT', 0),
        // These caps are shared by all authenticated accounts sending from
        // the same envelope-sender domain. Zero disables each cap.
        'submission_domain_daily_limit' => (int) env('GATEWAY_EMAIL_SUBMISSION_DOMAIN_DAILY_LIMIT', 0),
        'submission_domain_daily_byte_limit' => (int) env('GATEWAY_EMAIL_SUBMISSION_DOMAIN_DAILY_BYTE_LIMIT', 0),
        'submission_usage_retention_days' => (int) env('GATEWAY_EMAIL_SUBMISSION_USAGE_RETENTION_DAYS', 90),
        // Optional deployment-owned POP3 credential authority. It returns a
        // registered upstream ID, never a client-selected network address.
        'mailbox_authenticator' => env('GATEWAY_EMAIL_MAILBOX_AUTHENTICATOR'),
        // Explicit opt-in. Keys are a JSON array of base64-encoded 32+ byte
        // secrets shared by all Planner workers; the first signs new tokens.
        'srs_enabled' => (bool) env('GATEWAY_EMAIL_SRS_ENABLED', false),
        // Can stop issuing new tokens while old return addresses remain valid.
        'srs_rewrite_enabled' => (bool) env('GATEWAY_EMAIL_SRS_REWRITE_ENABLED', true),
        'srs_domain' => (string) env('GATEWAY_EMAIL_SRS_DOMAIN', ''),
        'srs_keys' => json_decode((string) (env('GATEWAY_EMAIL_SRS_KEYS') ?: '[]'), true),
        'srs_max_age_seconds' => (int) env('GATEWAY_EMAIL_SRS_MAX_AGE_SECONDS', 21 * 86400),
    ],

    'events' => [
        // Bounded Web ingress history is opt-in for high-volume deployments.
        // Bodies are retained only when endpoint metadata explicitly opts in.
        'enabled' => (bool) env('GATEWAY_EVENTS_ENABLED', false),
        'max_body_bytes' => (int) env('GATEWAY_EVENTS_MAX_BODY_BYTES', 65536),
        'retention_days' => (int) env('GATEWAY_EVENTS_RETENTION_DAYS', 30),
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

    'dns' => [
        // Setting GATEWAY_DNS_ZONE enables the PHP-owned DNS hook control
        // plane. Go uses the same variable for its authoritative listener.
        'enabled' => (bool) env('GATEWAY_DNS_HOOKS_ENABLED', env('GATEWAY_DNS_ZONE') !== null || env('GATEWAY_DNS_ZONES') !== null),
        'zone' => strtolower(trim((string) env('GATEWAY_DNS_ZONE', ''), ". \t\n\r\0\x0B")),
        'nameservers' => array_values(array_filter(array_map('trim', explode(',', (string) env('GATEWAY_DNS_NAMESERVERS', ''))))),
        'zones' => json_decode((string) (env('GATEWAY_DNS_ZONES') ?: '[]'), true),
        'client' => (string) env('GATEWAY_DNS_PROXY_CLIENT', 'gateway'),
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
