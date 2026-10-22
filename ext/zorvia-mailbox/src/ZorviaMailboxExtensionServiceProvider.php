<?php

declare(strict_types=1);

namespace Webong\Gateway\ZorviaMailbox;

use Illuminate\Support\ServiceProvider;

final class ZorviaMailboxExtensionServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $this->mergeConfigFrom(__DIR__.'/../config/zorvia-mailbox.php', 'gateway-zorvia-mailbox');
        $this->app->bind(ZorviaMailboxGrantTransport::class, CurlZorviaMailboxGrantTransport::class);
        $this->app['config']->set(
            'gateway.email.credential_source_adapters.'.ZorviaApplicationMailboxGrantSource::ADAPTER,
            ZorviaApplicationMailboxGrantSource::class,
        );
    }

    public function boot(): void
    {
        $this->publishes([
            __DIR__.'/../config/zorvia-mailbox.php' => config_path('gateway-zorvia-mailbox.php'),
        ], 'gateway-zorvia-mailbox-config');
    }
}
