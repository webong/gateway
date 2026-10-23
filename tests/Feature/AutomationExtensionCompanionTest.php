<?php

declare(strict_types=1);

use Webong\Gateway\Contract\AutomationCapability;
use Webong\Gateway\Contract\AutomationExecutionTarget;
use Webong\Gateway\Contract\AutomationLanguageRegistry;
use Webong\Gateway\JavaScript\JavaScriptExtensionServiceProvider;
use Webong\Gateway\JavaScript\JavaScriptLanguage;
use Webong\Gateway\Lua\LuaExtensionServiceProvider;
use Webong\Gateway\Lua\LuaLanguage;
use Webong\Gateway\Shell\ShellExtensionServiceProvider;
use Webong\Gateway\Shell\ShellLanguage;
use Webong\Gateway\WebhookScript\WebhookScriptExtensionServiceProvider;
use Webong\Gateway\WebhookScript\WebhookScriptLanguage;

it('discovers extension-owned Planner companions through Composer', function (): void {
    $registry = app(AutomationLanguageRegistry::class);

    expect(app()->providerIsLoaded(JavaScriptExtensionServiceProvider::class))->toBeTrue()
        ->and(app()->providerIsLoaded(LuaExtensionServiceProvider::class))->toBeTrue()
        ->and(app()->providerIsLoaded(ShellExtensionServiceProvider::class))->toBeTrue()
        ->and(app()->providerIsLoaded(WebhookScriptExtensionServiceProvider::class))->toBeTrue();

    foreach ([JavaScriptLanguage::ID, LuaLanguage::ID] as $language) {
        expect($registry->target($language))->toBe(AutomationExecutionTarget::SPINNER)
            ->and($registry->supports($language, AutomationCapability::ROUTE))->toBeTrue()
            ->and($registry->supports($language, AutomationCapability::SCHEDULE))->toBeTrue()
            ->and($registry->supports($language, AutomationCapability::CREDENTIAL))->toBeTrue();
    }

    expect($registry->target(ShellLanguage::ID))->toBe(AutomationExecutionTarget::SPINNER)
        ->and($registry->supports(ShellLanguage::ID, AutomationCapability::ROUTE))->toBeTrue()
        ->and($registry->supports(ShellLanguage::ID, AutomationCapability::SCHEDULE))->toBeFalse();

    expect($registry->target(WebhookScriptLanguage::ID))->toBe(AutomationExecutionTarget::PLANNER)
        ->and($registry->supports(WebhookScriptLanguage::ID, AutomationCapability::ROUTE))->toBeTrue()
        ->and($registry->supports(WebhookScriptLanguage::ID, AutomationCapability::SCHEDULE))->toBeFalse()
        ->and($registry->supports(WebhookScriptLanguage::ID, AutomationCapability::CREDENTIAL))->toBeTrue();
});
