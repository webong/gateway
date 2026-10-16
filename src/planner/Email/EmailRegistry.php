<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Illuminate\Support\Carbon;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Str;
use stdClass;

final readonly class EmailRegistry
{
    public function __construct(private TxtResolver $txt, private EmailRuntimePolicyStore $runtimePolicy) {}

    /** @return list<stdClass> */
    public function domains(): array
    {
        return DB::table('email_domains')->orderBy('domain')->get()->all();
    }

    public function domain(string $id): ?stdClass
    {
        return DB::table('email_domains')->where('id', $id)->first();
    }

    public function domainByName(string $name): ?stdClass
    {
        return DB::table('email_domains')->where('domain', $name)->first();
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
        $now = now();
        $id = (string) Str::uuid();
        DB::table('email_domains')->insert([
            'id' => $id,
            'domain' => strtolower(rtrim($name, '.')),
            'verification_token' => bin2hex(random_bytes(24)),
            'enabled' => false,
            'created_at' => $now,
            'updated_at' => $now,
        ]);

        return $this->domain($id);
    }

    public function verify(stdClass $domain): stdClass
    {
        $name = '_gateway-verify.'.$domain->domain;
        $expected = 'gateway-verification='.$domain->verification_token;
        foreach ($this->txt->records($name) as $record) {
            if (hash_equals($expected, trim($record))) {
                DB::table('email_domains')->where('id', $domain->id)->update([
                    'verified_at' => now(),
                    'updated_at' => now(),
                ]);

                return $this->domain($domain->id);
            }
        }

        throw new VerificationRecordMissing('The domain verification TXT record was not found.');
    }

    public function setEnabled(stdClass $domain, bool $enabled): stdClass
    {
        if ($enabled && ! $domain->enabled) {
            $this->verify($domain);
        }
        DB::table('email_domains')->where('id', $domain->id)->update([
            'enabled' => $enabled,
            'updated_at' => now(),
        ]);

        return $this->domain($domain->id);
    }

    public function deleteDomain(stdClass $domain): void
    {
        DB::table('email_domains')->where('id', $domain->id)->delete();
    }

    /** @return list<stdClass> */
    public function aliases(stdClass $domain): array
    {
        return DB::table('email_aliases')->where('domain_id', $domain->id)->orderBy('local_part')->get()->all();
    }

    public function alias(stdClass $domain, string $id): ?stdClass
    {
        return DB::table('email_aliases')->where('domain_id', $domain->id)->where('id', $id)->first();
    }

    public function aliasByLocalPart(stdClass $domain, string $localPart): ?stdClass
    {
        return DB::table('email_aliases')->where('domain_id', $domain->id)->where('local_part', $localPart)->first();
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
        $id = (string) Str::uuid();
        $now = now();
        DB::table('email_aliases')->insert([
            'id' => $id,
            'domain_id' => $domain->id,
            'local_part' => strtolower($localPart),
            'recipients' => json_encode(array_values($recipients), JSON_THROW_ON_ERROR),
            'enabled' => true,
            'created_at' => $now,
            'updated_at' => $now,
        ]);

        return $this->alias($domain, $id);
    }

    /** @param array{recipients?: list<string>, enabled?: bool} $attributes */
    public function updateAlias(stdClass $domain, stdClass $alias, array $attributes): stdClass
    {
        $updates = ['updated_at' => now()];
        if (array_key_exists('recipients', $attributes)) {
            $updates['recipients'] = json_encode(array_values($attributes['recipients']), JSON_THROW_ON_ERROR);
        }
        if (array_key_exists('enabled', $attributes)) {
            $updates['enabled'] = $attributes['enabled'];
        }
        DB::table('email_aliases')->where('id', $alias->id)->update($updates);

        return $this->alias($domain, $alias->id);
    }

    public function deleteAlias(stdClass $alias): void
    {
        DB::table('email_aliases')->where('id', $alias->id)->delete();
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
