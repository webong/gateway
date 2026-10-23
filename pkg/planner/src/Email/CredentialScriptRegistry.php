<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use RuntimeException;
use Webong\Gateway\Protocols\Delivery;

final class CredentialScriptRegistry
{
    /** @var array<string, CredentialScriptExecutor> */
    private array $executors = [];

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
        $executor = $this->executors[$language] ?? null;
        if ($executor === null) {
            throw new RuntimeException('Credential script language is unavailable.');
        }

        return $executor->execute($source, $event);
    }

    public function supports(string $language): bool
    {
        return isset($this->executors[$language]);
    }
}
