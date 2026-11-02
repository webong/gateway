<?php

declare(strict_types=1);

namespace Webong\Gateway\Shell;

use Webong\Gateway\Contracts\ExtensionSecretProvider;

/** Exposes SSH credentials only through Planner's identity-scoped secret API. */
final readonly class ShellExtensionSecretProvider implements ExtensionSecretProvider
{
    public const IDENTITY = 'webong/shell';

    public function __construct(private ShellSshExecutionStore $executions) {}

    public function identity(): string
    {
        return self::IDENTITY;
    }

    public function resolve(string $reference): ?array
    {
        if (! preg_match('/\Assh-([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\z/D', $reference, $match)) {
            return null;
        }
        $execution = $this->executions->resolve($match[1]);
        if ($execution === null || ! is_int($execution['credential_version'] ?? null)) {
            return null;
        }
        $version = $execution['credential_version'];
        unset($execution['credential_version']);

        return ['reference' => $reference, 'version' => $version, 'value' => $execution];
    }
}
