<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Illuminate\Support\Facades\DB;
use InvalidArgumentException;
use stdClass;
use Webong\Gateway\Resources\PlannerResourceStore;

/** Operator-selected TXT format, snapshotted on every claimed domain. */
final readonly class EmailVerificationProfile
{
    private const KIND = 'email.verification-profile';
    private const LEGACY = ['label' => '_gateway-verify', 'value_prefix' => 'gateway-verification='];

    public function __construct(private PlannerResourceStore $resources, private TxtResolver $txt) {}

    public function find(): ?stdClass
    {
        return $this->resources->byKey(self::KIND, 'default');
    }

    /** @return array{label: string, value_prefix: string} */
    public function current(): array
    {
        return $this->find()?->document ?? self::LEGACY;
    }

    /** @return array{label: string, value_prefix: string} */
    public function saved(array $document): array
    {
        return $document['verification_profile'] ?? self::LEGACY;
    }

    /** @return array{name: string, value: string} */
    public function record(string $domain, string $token, array $document): array
    {
        $profile = $this->saved($document);
        return ['name' => $profile['label'].'.'.$domain,
            'value' => $profile['value_prefix'].$token];
    }

    /** @return array{name: string, value: string} */
    public function proposedRecord(string $domain, string $token): array
    {
        return $this->record($domain, $token, ['verification_profile' => $this->current()]);
    }

    public function hasRecord(array $record): bool
    {
        return in_array($record['value'], array_map('trim', $this->txt->records($record['name'])), true);
    }

    /** @param array<string, mixed> $input */
    public function create(array $input): stdClass
    {
        $this->validate($input);
        return $this->resources->create(self::KIND, 'default', $input);
    }

    /** @param array<string, mixed> $input */
    public function replace(array $input, int $revision): ?stdClass
    {
        $this->validate($input);
        $row = $this->find();
        return $row === null ? null : $this->resources->replace(self::KIND, $row->id, 'default', $input, true, $revision);
    }

    private function validate(array $input): void
    {
        if (count($input) !== 2 || ! array_key_exists('label', $input) || ! array_key_exists('value_prefix', $input)
            || ! is_string($input['label']) || ! is_string($input['value_prefix'])
            || preg_match('/^_[a-z0-9](?:[a-z0-9-]{0,59}[a-z0-9])?$/D', $input['label']) !== 1
            || preg_match('/^[a-z][a-z0-9-]{0,39}=$/D', $input['value_prefix']) !== 1) {
            throw new InvalidArgumentException('Invalid Email verification profile.');
        }
    }

    /** Verify the new record first, then switch both sides of a shared domain in one transaction. */
    public function migrate(string $account, string $domain): void
    {
        $profile = $this->current();
        DB::transaction(function () use ($account, $domain, $profile): void {
            $matches = [];
            foreach (['email.receiving-domain', 'email.sending-domain'] as $kind) {
                foreach ($this->resources->all($kind) as $row) {
                    if (($row->document['account_id'] ?? null) === $account
                        && ($row->document['name'] ?? null) === $domain) {
                        $matches[] = [$kind, $row];
                    }
                }
            }
            if ($matches === []) { throw new InvalidArgumentException('Domain not found.'); }
            $token = $matches[0][1]->document['verification_token'];
            foreach ($matches as [, $row]) {
                if ($row->document['verification_token'] !== $token) {
                    throw new InvalidArgumentException('Shared domains have different verification tokens.');
                }
                if (($row->document['verified_at'] ?? null) === null) {
                    throw new InvalidArgumentException('Verify every shared domain before migrating.');
                }
            }
            $record = $this->record($domain, $token, ['verification_profile' => $profile]);
            if (! $this->hasRecord($record)) {
                throw new VerificationRecordMissing('The new domain verification TXT record was not found.');
            }
            foreach ($matches as [$kind, $row]) {
                $document = $row->document;
                $document['verification_profile'] = $profile;
                $document['verified_at'] = now('UTC')->toIso8601String();
                $this->resources->replace($kind, $row->id, $row->key, $document, $row->enabled, $row->revision);
            }
        });
    }
}
