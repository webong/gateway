<?php

declare(strict_types=1);

namespace Webong\Gateway\Centrifugo;

use Illuminate\Support\ServiceProvider;
use Webong\Gateway\Centrifugo\ServerTypes\CentrifugoServerType;
use Webong\Gateway\Servers\ServerTypeRegistry;

final class CentrifugoExtensionServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $this->app->singleton(CentrifugoServerType::class);
        $this->app->afterResolving(ServerTypeRegistry::class, function (ServerTypeRegistry $types): void {
            $types->register($this->app->make(CentrifugoServerType::class));
        });
    }
}
