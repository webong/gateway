<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\AutomationCapability;
use Webong\Gateway\Contracts\AutomationExecutionTarget;
use Webong\Gateway\Contracts\AutomationLanguageRegistry;
use Webong\Gateway\Contracts\CompanionActionRegistry;
use Webong\Gateway\HookScript\HookScriptCompanionAction;
use Webong\Gateway\HookScript\HookScriptExtensionServiceProvider;
use Webong\Gateway\HookScript\HookScriptLanguage;
use Webong\Gateway\JavaScript\JavaScriptExtensionServiceProvider;
use Webong\Gateway\JavaScript\JavaScriptLanguage;
use Webong\Gateway\Lua\LuaExtensionServiceProvider;
use Webong\Gateway\Lua\LuaLanguage;
use Webong\Gateway\Shell\ShellExtensionServiceProvider;
use Webong\Gateway\Shell\ShellLanguage;

it('discovers extension-owned Planner companions through Composer', function (): void {
    $registry = app(AutomationLanguageRegistry::class);

    expect(app()->providerIsLoaded(JavaScriptExtensionServiceProvider::class))->toBeTrue()
        ->and(app()->providerIsLoaded(LuaExtensionServiceProvider::class))->toBeTrue()
        ->and(app()->providerIsLoaded(ShellExtensionServiceProvider::class))->toBeTrue()
        ->and(app()->providerIsLoaded(HookScriptExtensionServiceProvider::class))->toBeTrue();

    foreach ([JavaScriptLanguage::ID, LuaLanguage::ID] as $language) {
        expect($registry->target($language))->toBe(AutomationExecutionTarget::SPINNER)
            ->and($registry->supports($language, AutomationCapability::ROUTE))->toBeTrue()
            ->and($registry->supports($language, AutomationCapability::SCHEDULE))->toBeTrue()
            ->and($registry->supports($language, AutomationCapability::CREDENTIAL))->toBeTrue();
    }

    expect($registry->target(ShellLanguage::ID))->toBe(AutomationExecutionTarget::SPINNER)
        ->and($registry->supports(ShellLanguage::ID, AutomationCapability::ROUTE))->toBeTrue()
        ->and($registry->supports(ShellLanguage::ID, AutomationCapability::SCHEDULE))->toBeFalse();

    expect($registry->target(HookScriptLanguage::ID))->toBe(AutomationExecutionTarget::SPINNER)
        ->and($registry->supports(HookScriptLanguage::ID, AutomationCapability::ROUTE))->toBeTrue()
        ->and($registry->supports(HookScriptLanguage::ID, AutomationCapability::SCHEDULE))->toBeTrue()
        ->and($registry->supports(HookScriptLanguage::ID, AutomationCapability::CREDENTIAL))->toBeTrue()
        ->and(app(CompanionActionRegistry::class)->capability(HookScriptCompanionAction::NAME)?->version)
        ->toBe(HookScriptCompanionAction::VERSION);
});
