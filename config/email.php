<?php

declare(strict_types=1);

return [
    'hook_client' => (string) env('GATEWAY_EMAIL_HOOK_CLIENT', 'gateway'),
    'provider_route_resolver' => env('GATEWAY_EMAIL_PROVIDER_ROUTE_RESOLVER'),
    'oauth_refresher' => env('GATEWAY_EMAIL_OAUTH_REFRESHER', \Webong\Gateway\Email\ProviderMailboxOAuthRefresher::class),
    'credential_source_adapters' => [
        'http' => \Webong\Gateway\Email\HttpApplicationMailboxGrantSource::class,
        'script' => \Webong\Gateway\Email\ScriptedApplicationMailboxGrantSource::class,
    ],
    'oauth' => [
        'callback_url' => env('GATEWAY_EMAIL_OAUTH_CALLBACK_URL'),
        'google' => [
            'client_id' => env('GATEWAY_EMAIL_GOOGLE_CLIENT_ID'),
            'client_secret' => env('GATEWAY_EMAIL_GOOGLE_CLIENT_SECRET'),
            'imap_upstream_id' => env('GATEWAY_EMAIL_GOOGLE_IMAP_UPSTREAM', 'google-imap'),
            'smtp_upstream_id' => env('GATEWAY_EMAIL_GOOGLE_SMTP_UPSTREAM', 'google-smtp'),
        ],
        'microsoft' => [
            'client_id' => env('GATEWAY_EMAIL_MICROSOFT_CLIENT_ID'),
            'client_secret' => env('GATEWAY_EMAIL_MICROSOFT_CLIENT_SECRET'),
            'tenant' => env('GATEWAY_EMAIL_MICROSOFT_TENANT', 'common'),
            'imap_upstream_id' => env('GATEWAY_EMAIL_MICROSOFT_IMAP_UPSTREAM', 'microsoft-imap'),
            'smtp_upstream_id' => env('GATEWAY_EMAIL_MICROSOFT_SMTP_UPSTREAM', 'microsoft-smtp'),
        ],
    ],
    'policy' => env('GATEWAY_EMAIL_POLICY'),
    'submission_authenticator' => env('GATEWAY_EMAIL_SUBMISSION_AUTHENTICATOR', \Webong\Gateway\Email\ConnectedSMTPAuthenticator::class),
    'shared_from_address' => env('GATEWAY_EMAIL_SHARED_FROM_ADDRESS', ''),
    'managed_dkim_signing_enabled' => (bool) env('GATEWAY_EMAIL_MANAGED_DKIM_SIGNING_ENABLED', false),
    'managed_dkim_selector' => env('GATEWAY_EMAIL_MANAGED_DKIM_SELECTOR', 'gateway1'),
    'managed_spf_value' => env('GATEWAY_EMAIL_MANAGED_SPF_VALUE', ''),
    'mailbox_authenticator' => env('GATEWAY_EMAIL_MAILBOX_AUTHENTICATOR', \Webong\Gateway\Email\ConnectedIMAPAuthenticator::class),
    'hosted_allowed_hosts' => [],
];
