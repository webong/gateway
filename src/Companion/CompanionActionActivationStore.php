<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion;

use InvalidArgumentException;
use stdClass;
use Webong\Gateway\Resources\PlannerResourceStore;

/** Planner-owned desired state for selecting one companion provider per extension. */
final readonly class CompanionActionActivationStore
{
    public const string KIND = 'extension';

    public function __construct(private PlannerResourceStore $resources) {}

    /** @return list<stdClass> */
    public function all(): array
    {
        return $this->resources->all(self::KIND);
    }

    public function find(string $id): ?stdClass
    {
        return $this->resources->find(self::KIND, $id);
    }

    public function byExtension(string $extension): ?stdClass
    {
        $this->validateExtension($extension);

        return $this->resources->byKey(self::KIND, $extension);
    }

    public function selection(string $extension): CompanionActionActivation
    {
        $row = $this->byExtension($extension);
        if ($row === null) {
            // Existing Composer packages remain active until an operator opts
            // the extension into dynamic process hosting through Planner.
            return new CompanionActionActivation($extension, true, 'static', null, null, 0, false);
        }

        $companion = $row->document['companion'] ?? null;
        if (! is_array($companion)) {
            throw new InvalidArgumentException("Gateway extension [{$extension}] has invalid companion configuration.");
        }
        $execution = $companion['execution'] ?? null;
        if ($execution !== null && ! is_array($execution)) {
            throw new InvalidArgumentException("Gateway extension [{$extension}] has invalid companion execution policy.");
        }

        return new CompanionActionActivation(
            extension: $extension,
            enabled: $row->enabled,
            mode: $companion['mode'],
            version: $companion['version'] ?? null,
            digest: $companion['digest'] ?? null,
            generation: $row->revision,
            configured: true,
            execution: CompanionExecutionPolicy::fromDocument(
                $execution,
                $row->revision,
            ),
        );
    }

    /** @param array<string, mixed> $input */
    public function create(array $input): stdClass
    {
        [$extension, $enabled, $document] = $this->validated($input);

        return $this->resources->create(self::KIND, $extension, $document, $enabled);
    }

    /** @param array<string, mixed> $input */
    public function replace(string $id, array $input, int $revision): ?stdClass
    {
        [$extension, $enabled, $document] = $this->validated($input);
        $current = $this->find($id);
        if ($current === null) {
            return null;
        }
        if (! hash_equals($current->key, $extension)) {
            throw new InvalidArgumentException('A Gateway extension identity cannot be changed.');
        }

        return $this->resources->replace(self::KIND, $id, $extension, $document, $enabled, $revision);
    }

    public function delete(string $id, int $revision): bool
    {
        return $this->resources->delete(self::KIND, $id, $revision);
    }

    /**
     * @param array<string, mixed> $input
     * @return array{string, bool, array{companion: array{mode: string, version?: string, digest?: string, execution: array<string, mixed>}}}
     */
    private function validated(array $input): array
    {
        if (array_diff(array_keys($input), ['extension', 'enabled', 'mode', 'version', 'digest', 'execution']) !== []) {
            throw new InvalidArgumentException('Invalid Gateway extension activation.');
        }
        $extension = $input['extension'] ?? null;
        $enabled = $input['enabled'] ?? true;
        $mode = $input['mode'] ?? null;
        $version = $input['version'] ?? null;
        $digest = $input['digest'] ?? null;
        $execution = $input['execution'] ?? null;
        if (! is_string($extension) || ! is_bool($enabled) || ! is_string($mode)
            || ! in_array($mode, ['static', 'dynamic'], true)
            || ($version !== null && (! is_string($version) || trim($version) === '' || strlen($version) > 64))
            || ($digest !== null && (! is_string($digest) || preg_match('/\A[a-f0-9]{64}\z/D', $digest) !== 1))
            || ($execution !== null && ! is_array($execution))) {
            throw new InvalidArgumentException('Invalid Gateway extension activation.');
        }
        $this->validateExtension($extension);
        $policy = CompanionExecutionPolicy::fromDocument($execution);
        if ($mode !== 'dynamic' && $policy->mode !== CompanionExecutionPolicy::ISOLATED) {
            throw new InvalidArgumentException('Only dynamic Gateway companions may use warmed execution.');
        }

        $companion = ['mode' => $mode, 'execution' => $policy->toArray()];
        if ($version !== null) {
            $companion['version'] = $version;
        }
        if ($digest !== null) {
            $companion['digest'] = $digest;
        }

        return [$extension, $enabled, ['companion' => $companion]];
    }

    private function validateExtension(string $extension): void
    {
        if (preg_match('/\A[a-z0-9][a-z0-9._\/-]{0,255}\z/D', $extension) !== 1) {
            throw new InvalidArgumentException('Invalid Gateway extension identity.');
        }
    }
}
