<?php

declare(strict_types=1);

namespace Webong\Gateway\Tests\Fixtures;

use Webong\Gateway\Contracts\CompanionActionProvider;
use Webong\Gateway\Protocols\CompanionActionCapability;
use Webong\Gateway\Protocols\CompanionActionInvocation;
use Webong\Gateway\Protocols\CompanionActionResult;

final readonly class FakeDynamicCompanionProvider implements CompanionActionProvider
{
    public function __construct(
        private string $action,
        private string $extensionId,
        private string $id = 'dynamic:fixture',
        private string $version = '1.0.0',
    ) {}

    public function extension(): string
    {
        return $this->extensionId;
    }

    public function mode(): string
    {
        return 'dynamic';
    }

    public function providerId(): string
    {
        return $this->id;
    }

    public function capability(): CompanionActionCapability
    {
        return new CompanionActionCapability($this->action, $this->version);
    }

    public function invoke(CompanionActionInvocation $invocation): CompanionActionResult
    {
        return CompanionActionResult::succeeded($invocation->executionId, [
            'provider' => 'dynamic',
            'subject' => $invocation->input['subject'] ?? null,
        ]);
    }
}
