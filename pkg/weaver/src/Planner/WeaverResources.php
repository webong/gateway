<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver\Planner;

use InvalidArgumentException;
use stdClass;
use Webong\Gateway\Laravel\Accounts\AccountStore;
use Webong\Gateway\Laravel\Resources\PlannerResourceStore;
use Webong\Gateway\Weaver\{ApprovedOperations, PublishedDefinitions, RunGrant, RuntimeConfiguration};
use Webong\WorkFlow\Services\CanonicalFlowPayload;

/** Desired state only. Executions, attempts, callbacks and outbox stay operational. */
final readonly class WeaverResources
{
    public const DEFINITION = 'weaver.definition';
    public const GRANT = 'weaver.grant';

    public function __construct(private PlannerResourceStore $resources, private AccountStore $accounts) {}

    public function definitions(string $account): array
    {
        return $this->resources->allOwned(self::DEFINITION, $account);
    }

    public function grants(string $account): array
    {
        return $this->resources->allOwned(self::GRANT, $account);
    }

    public function publish(string $account, array $document): stdClass
    {
        $this->requireAccount($account);
        if (strlen(json_encode($document, JSON_THROW_ON_ERROR)) > 32768) {throw new InvalidArgumentException('Published definitions must fit within 32 KiB.');}
        if (array_diff(array_keys($document), ['key', 'version', 'steps', 'metadata']) !== []) {
            throw new InvalidArgumentException('Unsupported definition properties.');
        }
        $definitions = new PublishedDefinitions([$document], new ApprovedOperations(ApprovedOperations::KEYS));
        $definition = $definitions->definition($document['key'])->toArray();
        $key = $definition['key'].'@'.$definition['version'];
        $existing = $this->resources->byOwnedKey(self::DEFINITION, $account, $key);
        if ($existing !== null) {
            if (CanonicalFlowPayload::fingerprint($existing->document) !== CanonicalFlowPayload::fingerprint($definition)) {
                throw new ResourceConflict('Published definition versions are immutable; publish a new version.');
            }
            return $existing;
        }
        return $this->resources->create(self::DEFINITION, $key, $definition, owner: $account);
    }

    public function createGrant(string $account, array $document): stdClass
    {
        $grant = $this->validateGrant($account, $document);
        $key = $this->grantKey($grant);
        $existing = $this->resources->byOwnedKey(self::GRANT, $account, $key);
        if ($existing !== null) {
            if (CanonicalFlowPayload::fingerprint($existing->document) !== CanonicalFlowPayload::fingerprint($grant->toArray())) {
                throw new ResourceConflict('A grant already exists for this scope; replace it with If-Match.');
            }
            return $existing;
        }
        return $this->resources->create(self::GRANT, $key, $grant->toArray(), owner: $account);
    }

    public function replaceGrant(string $account, string $id, array $document, int $revision): ?stdClass
    {
        $grant = $this->validateGrant($account, $document);
        return $this->resources->replace(self::GRANT, $id, $this->grantKey($grant), $grant->toArray(), true, $revision, $account);
    }

    public function revokeGrant(string $account, string $id, int $revision): bool
    {
        $this->requireAccount($account);
        return $this->resources->delete(self::GRANT, $id, $revision, $account);
    }

    /** Base configuration selects immutable definition IDs, never inline application code. */
    public function project(array $base): array
    {
        $selections = $base['accounts'] ?? null;
        if (!is_array($selections) || !array_is_list($selections) || $selections === []) {
            throw new InvalidArgumentException('Select explicit Weaver accounts and published definition IDs.');
        }
        $accounts = [];
        foreach ($selections as $selection) {
            if (!is_array($selection) || array_diff(array_keys($selection), ['id', 'definitions']) !== []
                || !is_string($selection['id'] ?? null) || !is_array($selection['definitions'] ?? null)
                || !array_is_list($selection['definitions']) || $selection['definitions'] === []) {
                throw new InvalidArgumentException('Invalid Weaver definition selection.');
            }
            $account = $selection['id'];
            $this->requireAccount($account);
            $definitions = [];
            $keys = [];
            foreach ($selection['definitions'] as $id) {
                if (!is_string($id)) { throw new InvalidArgumentException('Select a published definition ID.'); }
                $definition = $this->resources->findOwned(self::DEFINITION, $id, $account);
                if ($definition === null || !$definition->enabled) {
                    throw new InvalidArgumentException('Selected definition is missing or belongs to another account.');
                }
                $definitions[] = $definition->document;
                $keys[$definition->document['key']] = true;
            }
            $grants = [];
            foreach ($this->grants($account) as $grant) {
                if ($grant->enabled && isset($keys[$grant->document['flow_key'] ?? ''])) {
                    $grants[] = $grant->document;
                }
            }
            $accounts[] = ['id' => $account, 'definitions' => $definitions, 'grants' => $grants];
        }
        $base['accounts'] = $accounts;
        new RuntimeConfiguration($base);
        if(strlen(json_encode($base, JSON_THROW_ON_ERROR))>65536){throw new InvalidArgumentException('Selected Weaver configuration exceeds 64 KiB.');}
        return $base;
    }

    private function validateGrant(string $account, array $document): RunGrant
    {
        $this->requireAccount($account);
        if (array_diff(array_keys($document), ['flow_key', 'subject', 'actions']) !== []) {
            throw new InvalidArgumentException('Grant ownership comes from the account route.');
        }
        $grant = RunGrant::fromArray(['account' => $account, ...$document]);
        foreach ($this->definitions($account) as $definition) {
            if ($definition->enabled && ($definition->document['key'] ?? null) === $grant->flowKey) { return $grant; }
        }
        throw new InvalidArgumentException('Publish a definition for this account before granting run control.');
    }

    private function grantKey(RunGrant $grant): string
    {
        return hash('sha256', json_encode([$grant->flowKey, $grant->subjectType, $grant->subjectId], JSON_THROW_ON_ERROR));
    }

    private function requireAccount(string $account): void
    {
        if (preg_match('/\A[A-Za-z0-9._:-]{1,200}\z/D', $account) !== 1 || !$this->accounts->exists($account)) {
            throw new InvalidArgumentException('Weaver account does not exist.');
        }
    }
}
