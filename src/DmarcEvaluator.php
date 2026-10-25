<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Throwable;
use Webong\Gateway\Protocols\GatewayEvent;

final readonly class DmarcEvaluator
{
    public function __construct(private TxtResolver $txt) {}

    public function evaluate(GatewayEvent $event): DmarcAssessment
    {
        $from = strtolower($event->attributes['rfc5322_from_domain'] ?? '');
        if ($from === '') {
            return new DmarcAssessment('unavailable');
        }

        $cache = [];
        try {
            [$policyDomain, $tags] = $this->discoverPolicy($from, $cache);
            if ($tags === null) {
                return new DmarcAssessment('none');
            }
            $organization = $this->organizationFor($from, $cache);
        } catch (Throwable) {
            return new DmarcAssessment('temperror');
        }

        $alignmentUncertain = false;
        try {
            $spfAligned = $this->spfAligned($event, $tags, $from, $organization, $cache);
        } catch (Throwable) {
            $spfAligned = false;
            $alignmentUncertain = true;
        }
        try {
            $dkimAligned = $this->dkimAligned($event, $tags, $from, $organization, $cache);
        } catch (Throwable) {
            $dkimAligned = false;
            $alignmentUncertain = true;
        }

        $existence = $event->attributes['rfc5322_from_existence'] ?? 'unavailable';
        $policyUncertain = $policyDomain !== $from && isset($tags['np'])
            && ! in_array($existence, ['exists', 'nxdomain'], true);
        $policy = $policyDomain !== $from
            ? ($existence === 'nxdomain' ? ($tags['np'] ?? $tags['sp'] ?? $tags['p']) : ($tags['sp'] ?? $tags['p']))
            : $tags['p'];
        $testing = ($tags['t'] ?? 'n') === 'y';
        $effectivePolicy = $testing ? match ($policy) {
            'reject' => 'quarantine',
            'quarantine' => 'none',
            default => 'none',
        } : $policy;
        $result = $spfAligned || $dkimAligned ? 'pass' : 'fail';
        if ($result === 'fail' && ($policyUncertain || $alignmentUncertain || $this->uncertainAuthentication($event))) {
            $result = 'temperror';
        }

        return new DmarcAssessment($result, $policy, $policyDomain, $spfAligned, $dkimAligned, $effectivePolicy, $testing);
    }

    /** @return array{string, array<string, string>|null} */
    private function discoverPolicy(string $from, array &$cache): array
    {
        $tags = $this->singleRecord($from, $cache);
        if ($tags !== null) {
            return [$from, $tags];
        }

        $selectedDomain = '';
        $selectedTags = null;
        $candidates = [];
        $previous = $from;
        foreach (array_slice($this->walkTargets($from), 1) as $domain) {
            $candidate = $this->singleRecord($domain, $cache);
            if ($candidate !== null) {
                $candidates[$domain] = $candidate;
                $selectedDomain = $domain;
                $selectedTags = $candidate;
                if (($candidate['psd'] ?? '') === 'n') {
                    return [$domain, $candidate];
                }
                if (($candidate['psd'] ?? '') === 'y') {
                    // An organizational-domain record takes precedence over
                    // the public suffix's fallback policy.
                    return isset($candidates[$previous])
                        ? [$previous, $candidates[$previous]]
                        : [$domain, $candidate];
                }
            }
            $previous = $domain;
        }

        return [$selectedDomain, $selectedTags];
    }

    /** @return list<string> */
    private function walkTargets(string $domain): array
    {
        $targets = [$domain];
        $labels = explode('.', $domain);
        // RFC 9989's shortcut bounds each walk to eight DNS queries.
        $parent = count($labels) > 8 ? array_slice($labels, -7) : array_slice($labels, 1);
        while ($parent !== []) {
            $targets[] = implode('.', $parent);
            array_shift($parent);
        }

        return $targets;
    }

    /** @param array<string, array<string, string>|null> $cache */
    private function organizationFor(string $domain, array &$cache): string
    {
        $selected = '';
        $previous = $domain;
        foreach ($this->walkTargets($domain) as $target) {
            $tags = $this->singleRecord($target, $cache);
            if ($tags !== null) {
                if (($tags['psd'] ?? '') === 'n') {
                    return $target;
                }
                if (($tags['psd'] ?? '') === 'y' && $target !== $domain) {
                    return $previous;
                }
                $selected = $target;
            }
            $previous = $target;
        }

        return $selected !== '' ? $selected : $domain;
    }

    /** @return array<string, string>|null */
    private function singleRecord(string $domain, array &$cache): ?array
    {
        if (array_key_exists($domain, $cache)) {
            return $cache[$domain];
        }
        $records = $this->records($domain);

        return $cache[$domain] = count($records) === 1 ? $this->parseRecord($records[0]) : null;
    }

    /** @return list<string> */
    private function records(string $domain): array
    {
        return array_values(array_filter($this->txt->records('_dmarc.'.$domain), static function (string $record): bool {
            $firstTag = trim(explode(';', $record, 2)[0]);

            return preg_match('/^[vV]\s*=\s*DMARC1$/', $firstTag) === 1;
        }));
    }

    /** @return array<string, string>|null */
    private function parseRecord(string $record): ?array
    {
        $tags = [];
        foreach (explode(';', $record) as $part) {
            $part = trim($part);
            if ($part === '') {
                continue;
            }
            if (! str_contains($part, '=')) {
                return null;
            }
            [$key, $value] = array_map('trim', explode('=', $part, 2));
            $key = strtolower($key);
            if (isset($tags[$key]) && in_array($key, ['v', 'p', 'sp', 'np', 'psd', 'aspf', 'adkim', 't'], true)) {
                return null;
            }
            $tags[$key] = $value;
        }
        if (array_key_first($tags) !== 'v' || ($tags['v'] ?? null) !== 'DMARC1') {
            return null;
        }
        foreach (['p', 'sp', 'np'] as $policyTag) {
            if (isset($tags[$policyTag]) && ! in_array($tags[$policyTag], ['none', 'quarantine', 'reject'], true)) {
                if (! $this->hasReportingURI($tags['rua'] ?? '')) {
                    return null;
                }
                $tags['p'] = 'none';
                unset($tags['sp'], $tags['np']);
                break;
            }
        }
        if (! isset($tags['p'])) {
            if (! $this->hasReportingURI($tags['rua'] ?? '')) {
                return null;
            }
            $tags['p'] = 'none';
        }
        if (isset($tags['psd']) && ! in_array($tags['psd'], ['y', 'n', 'u'], true)) {
            unset($tags['psd']);
        }
        foreach (['aspf', 'adkim'] as $mode) {
            if (isset($tags[$mode]) && ! in_array($tags[$mode], ['r', 's'], true)) {
                unset($tags[$mode]);
            }
        }
        if (isset($tags['t']) && ! in_array($tags['t'], ['y', 'n'], true)) {
            unset($tags['t']);
        }

        return $tags;
    }

    private function hasReportingURI(string $value): bool
    {
        foreach (explode(',', $value) as $uri) {
            $uri = trim($uri);
            if (preg_match('/^mailto:[^@\s,]+@[^@\s,!]+(?:![0-9]+[kmgt]?)?$/i', $uri)
                || filter_var($uri, FILTER_VALIDATE_URL)) {
                return true;
            }
        }

        return false;
    }

    /** @param array<string, string> $tags */
    private function spfAligned(GatewayEvent $event, array $tags, string $from, string $organization, array &$cache): bool
    {
        $attributes = $event->attributes;
        if (($attributes['spf_result'] ?? '') !== 'pass') {
            return false;
        }
        if (($attributes['spf_identity'] ?? '') === 'helo' && ($attributes['mail_from'] ?? '') !== '') {
            return false;
        }

        return $this->aligned(
            $attributes['spf_domain'] ?? '',
            $from,
            $organization,
            $tags['aspf'] ?? 'r',
            $cache,
        );
    }

    /** @param array<string, string> $tags */
    private function dkimAligned(GatewayEvent $event, array $tags, string $from, string $organization, array &$cache): bool
    {
        $signatures = json_decode($event->attributes['dkim_signatures'] ?? '[]', true);
        if (! is_array($signatures) || ! array_is_list($signatures)) {
            return false;
        }
        foreach (array_slice($signatures, 0, 5) as $signature) {
            if (! is_array($signature) || ($signature['result'] ?? '') !== 'pass') {
                continue;
            }
            if ($this->aligned(
                (string) ($signature['domain'] ?? ''),
                $from,
                $organization,
                $tags['adkim'] ?? 'r',
                $cache,
            )) {
                return true;
            }
        }

        return false;
    }

    private function aligned(string $domain, string $from, string $organization, string $mode, array &$cache): bool
    {
        if ($domain === '') {
            return false;
        }
        if (strcasecmp($domain, $from) === 0) {
            return true;
        }
        if ($mode === 's') {
            return false;
        }

        return strcasecmp($this->organizationFor($domain, $cache), $organization) === 0;
    }

    private function uncertainAuthentication(GatewayEvent $event): bool
    {
        $attributes = $event->attributes;

        return in_array($attributes['spf_result'] ?? 'unavailable', ['temperror', 'unavailable'], true)
            || in_array($attributes['dkim_result'] ?? 'unavailable', ['temperror', 'unavailable'], true)
            || ($attributes['dkim_truncated'] ?? '') === 'true';
    }
}
