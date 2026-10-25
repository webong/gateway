<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use RuntimeException;
use Webong\Gateway\Contracts\AutomationCapability;
use Webong\Gateway\Contracts\AutomationLanguageRegistry;
use Webong\Gateway\Deliveries\DeliveryAdminClient;
use Webong\Gateway\Protocols\Delivery;

final class CredentialScriptRegistry
{
    /** @var array<string, CredentialScriptExecutor> */
    private array $executors = [];

    public function __construct(
        private readonly ?AutomationLanguageRegistry $languages = null,
        private readonly ?DeliveryAdminClient $admin = null,
    ) {}

    public function register(string $language, CredentialScriptExecutor $executor): void
    {
        if (preg_match('/^[a-z][a-z0-9_-]*$/D', $language) !== 1) {
            throw new RuntimeException('Credential script language is invalid.');
        }

        $this->executors[$language] = $executor;
    }

    /** @param array<string, string> $event */
    public function execute(string $language, string $source, array $event): Delivery
    {
        $executor = $this->executor($language);
        if ($executor === null) {
            throw new RuntimeException('Credential script language is unavailable.');
        }

        return $executor->execute($source, $event);
    }

    public function supports(string $language): bool
    {
        return $this->executor($language) !== null;
    }

    private function executor(string $language): ?CredentialScriptExecutor
    {
        $executor = $this->executors[$language] ?? null;
        if ($executor !== null) {
            return $executor;
        }
        if ($this->languages?->supports($language, AutomationCapability::CREDENTIAL) !== true || $this->admin === null) {
            return null;
        }

        return $this->executors[$language] = new SpinnerCredentialScriptExecutor($this->admin, $language);
    }
}
