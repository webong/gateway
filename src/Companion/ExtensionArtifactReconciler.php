<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion;

use RuntimeException;
use Throwable;

/** Materializes every enabled dynamic extension, independent of capability. */
final class ExtensionArtifactReconciler
{
    private ?string $signature = null;

    /** @var list<string> */
    private array $desired = [];

    public function __construct(
        private readonly CompanionActionActivationStore $activations,
        private readonly ExtensionArtifactInstaller $installer,
    ) {}

    /** @return list<string> */
    public function reconcile(): array
    {
        $rows = array_values(array_filter($this->activations->all(), static function (object $row): bool {
            $extension = $row->document['companion'] ?? null;

            return $row->enabled && is_array($extension) && ($extension['mode'] ?? null) === 'dynamic'
                && is_array($extension['artifact'] ?? null);
        }));
        $signature = hash('sha256', json_encode(array_map(static fn (object $row): array => [
            $row->key, $row->revision, $row->document['companion']['artifact']['sha256'],
        ], $rows), JSON_THROW_ON_ERROR));
        if ($signature !== $this->signature) {
            foreach ($rows as $row) {
                try {
                    $this->installer->install($row);
                } catch (Throwable $exception) {
                    throw new RuntimeException(
                        "Unable to reconcile Gateway extension [{$row->key}].",
                        previous: $exception,
                    );
                }
            }
            $this->desired = array_values(array_map(static fn (object $row): string => $row->key, $rows));
            sort($this->desired, SORT_STRING);
            $this->signature = $signature;
        }

        return $this->desired;
    }

    public function signature(): string
    {
        $this->reconcile();

        return $this->signature ?? hash('sha256', '[]');
    }
}
