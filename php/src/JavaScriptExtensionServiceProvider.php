<?php

declare(strict_types=1);

namespace Webong\Gateway\JavaScript;

use Illuminate\Support\ServiceProvider;
use Webong\Gateway\Contracts\AutomationCapability;
use Webong\Gateway\Contracts\AutomationExecutionTarget;
use Webong\Gateway\Contracts\AutomationLanguageRegistry;
use Webong\Gateway\Email\CredentialScriptRegistry;
use Webong\Gateway\Email\SpinnerCredentialScriptExecutor;

final class JavaScriptExtensionServiceProvider extends ServiceProvider
{
    public function boot(): void
    {
        $this->app->make(AutomationLanguageRegistry::class)->register(
            JavaScriptLanguage::ID,
            AutomationExecutionTarget::SPINNER,
            AutomationCapability::ROUTE,
            AutomationCapability::SCHEDULE,
            AutomationCapability::CREDENTIAL,
        );
        $this->app->make(CredentialScriptRegistry::class)->register(
            JavaScriptLanguage::ID,
            new SpinnerCredentialScriptExecutor($this->app->make(\Webong\Gateway\Deliveries\DeliveryAdminClient::class), JavaScriptLanguage::ID),
        );
    }
}
