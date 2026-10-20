<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Illuminate\Support\Carbon;
use Illuminate\Support\Facades\DB;
use InvalidArgumentException;
use stdClass;
use Webong\Gateway\Resources\PlannerResourceStore;

final readonly class EmailRegistry
{
    public function __construct(private TxtResolver $txt, private EmailRuntimePolicyStore $runtimePolicy,
        private PlannerResourceStore $resources) {}

    /** @return list<stdClass> */
    public function domains(): array
    {
        return array_map($this->domainRecord(...), $this->resources->all('email.domain'));
    }

    public function domain(string $id): ?stdClass
    {
        return $this->domainRecord($this->resources->find('email.domain', $id));
    }

    public function domainByName(string $name): ?stdClass
    {
        return $this->domainRecord($this->resources->byKey('email.domain', $name));
    }

    public function requireActiveDomain(string $name): stdClass
    {
        $domain = $this->domainByName(strtolower(rtrim($name, '.')));
        if ($domain === null || ! $domain->enabled || $domain->verified_at === null) {
            throw new EmailRouteRejected('The email domain is unavailable.');
        }
        $ttl = $this->runtimePolicy->current()['verification_ttl_seconds'];
        if ($ttl === 0 || Carbon::parse($domain->verified_at)->lte(now()->subSeconds($ttl))) {
            return $this->verify($domain);
        }

        return $domain;
    }

    public function createDomain(string $name): stdClass
    {
        $name = strtolower(rtrim($name, '.'));
        if ($this->resources->byKey('email.receiving-domain', $name) !== null) {
            throw new InvalidArgumentException('The email domain is already claimed for receiving.');
        }
        return $this->domainRecord($this->resources->create('email.domain', $name, [
            'verification_token' => bin2hex(random_bytes(24)), 'verified_at' => null,
        ], false));
    }

    public function verify(stdClass $domain): stdClass
    {
        $name = '_gateway-verify.'.$domain->domain;
        $expected = 'gateway-verification='.$domain->verification_token;
        foreach ($this->txt->records($name) as $record) {
            if (hash_equals($expected, trim($record))) {
                return $this->domainRecord($this->resources->replace('email.domain', $domain->id, $domain->domain,
                    ['verification_token' => $domain->verification_token, 'verified_at' => now('UTC')->toIso8601String()],
                    $domain->enabled, $domain->revision));
            }
        }

        throw new VerificationRecordMissing('The domain verification TXT record was not found.');
    }

    public function setEnabled(stdClass $domain, bool $enabled): stdClass
    {
        if ($enabled && ! $domain->enabled) {
            $domain = $this->verify($domain);
        }
        return $this->domainRecord($this->resources->replace('email.domain', $domain->id, $domain->domain,
            ['verification_token' => $domain->verification_token, 'verified_at' => $domain->verified_at],
            $enabled, $domain->revision));
    }

    public function deleteDomain(stdClass $domain): void
    {
        DB::transaction(function () use ($domain): void {
            foreach ($this->aliases($domain) as $alias) {
                $this->deleteAlias($alias);
            }
            $this->resources->delete('email.domain', $domain->id, $domain->revision);
        });
    }

    /** @return list<stdClass> */
    public function aliases(stdClass $domain): array
    {
        return array_map($this->aliasRecord(...), $this->resources->children('email.alias', $domain->id));
    }

    public function alias(stdClass $domain, string $id): ?stdClass
    {
        $alias = $this->resources->find('email.alias', $id);
        return $alias !== null && $alias->parent_id === $domain->id ? $this->aliasRecord($alias) : null;
    }

    public function aliasByLocalPart(stdClass $domain, string $localPart): ?stdClass
    {
        return $this->aliasRecord($this->resources->byKey('email.alias', $domain->id.'/'.$localPart));
    }

    /** A matching alias, including a paused one, reserves this address from catch-all hooks. */
    public function hasAliasMatch(string $address): bool
    {
        $at = strrpos($address, '@');
        if ($at === false) { return false; }
        $domain = $this->domainByName(strtolower(rtrim(substr($address, $at + 1), '.')));
        if ($domain === null) { return false; }
        $local = strtolower(substr($address, 0, $at));
        foreach ($this->aliases($domain) as $alias) {
            if ($alias->local_part === $local || $alias->local_part === '*'
                || (str_ends_with($alias->local_part, '*')
                    && str_starts_with($local, substr($alias->local_part, 0, -1)))) {
                return true;
            }
        }

        return false;
    }

    /** @param list<string> $recipients */
    public function createAlias(stdClass $domain, string $localPart, array $recipients): stdClass
    {
        $localPart = strtolower($localPart);
        return $this->aliasRecord($this->resources->create('email.alias', $domain->id.'/'.$localPart,
            ['recipients' => array_values($recipients)], true, $domain->id));
    }

    /** @param array{recipients?: list<string>, enabled?: bool} $attributes */
    public function updateAlias(stdClass $domain, stdClass $alias, array $attributes): stdClass
    {
        $recipients = array_key_exists('recipients', $attributes) ? array_values($attributes['recipients'])
            : json_decode($alias->recipients, true, 512, JSON_THROW_ON_ERROR);
        return $this->aliasRecord($this->resources->replace('email.alias', $alias->id,
            $domain->id.'/'.$alias->local_part, ['recipients' => $recipients],
            $attributes['enabled'] ?? $alias->enabled, $alias->revision));
    }

    public function deleteAlias(stdClass $alias): void
    {
        $this->resources->delete('email.alias', $alias->id, $alias->revision);
    }

    private function domainRecord(?stdClass $resource): ?stdClass
    {
        if ($resource === null) { return null; }
        return (object) [
            'id' => $resource->id, 'domain' => $resource->key, 'enabled' => $resource->enabled,
            'verification_token' => $resource->document['verification_token'],
            'verified_at' => $resource->document['verified_at'], 'revision' => $resource->revision,
        ];
    }

    private function aliasRecord(?stdClass $resource): ?stdClass
    {
        if ($resource === null) { return null; }
        return (object) [
            'id' => $resource->id, 'domain_id' => $resource->parent_id,
            'local_part' => substr($resource->key, strlen($resource->parent_id) + 1),
            'recipients' => json_encode($resource->document['recipients'], JSON_THROW_ON_ERROR),
            'enabled' => $resource->enabled, 'revision' => $resource->revision,
        ];
    }

    /**
     * Resolve one SMTP transaction, or return null when no recipient belongs
     * to a managed domain. A transaction cannot be partly accepted after DATA.
     *
     * @param list<string> $addresses
     * @return list<string>|null
     */
    public function resolveRecipients(array $addresses): ?array
    {
        $destinations = [];
        $managed = 0;
        $unmanaged = 0;
        /** @var array<string, true> $checkedDomains */
        $checkedDomains = [];
        /** @var array<string, array{exact: array<string, stdClass>, patterns: list<stdClass>, catch_all: ?stdClass}> $aliasCache */
        $aliasCache = [];
        foreach ($addresses as $address) {
            $separator = strrpos($address, '@');
            if ($separator === false) {
                throw new EmailRouteRejected('Invalid SMTP recipient.');
            }
            $local = strtolower(substr($address, 0, $separator));
            $name = strtolower(rtrim(substr($address, $separator + 1), '.'));
            $domain = $this->domainByName($name);
            if ($domain === null) {
                $unmanaged++;

                continue;
            }
            $managed++;
            if (preg_match('/^srs[01]-/i', $local) === 1) {
                throw new EmailRouteRejected('The return address is reserved.');
            }
            if (! isset($checkedDomains[$domain->id])) {
                $this->requireActiveDomain($name);
                $checkedDomains[$domain->id] = true;
            }
            if (! isset($aliasCache[$domain->id])) {
                $exact = [];
                $patterns = [];
                $catchAll = null;
                foreach ($this->aliases($domain) as $candidate) {
                    if ($candidate->local_part === '*') {
                        $catchAll = $candidate;
                    } elseif (str_ends_with($candidate->local_part, '*')) {
                        $patterns[] = $candidate;
                    } else {
                        $exact[$candidate->local_part] = $candidate;
                    }
                }
                $aliasCache[$domain->id] = ['exact' => $exact, 'patterns' => $patterns, 'catch_all' => $catchAll];
            }
            $aliases = $aliasCache[$domain->id];
            $alias = $aliases['exact'][$local] ?? null;
            if ($alias === null) {
                $longest = 0;
                foreach ($aliases['patterns'] as $pattern) {
                    $prefix = substr($pattern->local_part, 0, -1);
                    if (strlen($prefix) > $longest && str_starts_with($local, $prefix)) {
                        $alias = $pattern;
                        $longest = strlen($prefix);
                    }
                }
            }
            $alias ??= $aliases['catch_all'];
            if ($alias === null || ! $alias->enabled) {
                throw new EmailRouteRejected('The email recipient is unavailable.');
            }
            foreach (json_decode($alias->recipients, true, 512, JSON_THROW_ON_ERROR) as $target) {
                $targetDomain = substr($target, (int) strrpos($target, '@') + 1);
                if ($this->domainByName(strtolower($targetDomain)) !== null) {
                    throw new EmailRouteRejected('Forwarding back into a managed email domain is not allowed.');
                }
                $destinations[$target] = $target;
            }
        }
        if ($managed === 0) {
            return null;
        }
        if ($unmanaged > 0) {
            throw new EmailRouteRejected('Managed and unmanaged recipients cannot share one SMTP transaction.');
        }
        if (count($destinations) > 100) {
            throw new EmailRouteRejected('The email transaction has too many forwarding recipients.');
        }

        return array_values($destinations);
    }
}
