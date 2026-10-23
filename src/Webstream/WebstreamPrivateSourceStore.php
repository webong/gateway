<?php

declare(strict_types=1);

namespace Webong\Gateway\Webstream;

use InvalidArgumentException;
use stdClass;
use Webong\Gateway\Resources\PlannerResourceStore;

/** Operator-managed grants. The Spinner separately enforces its deployed network ceiling. */
final class WebstreamPrivateSourceStore
{
    private const KIND = 'webstream.private-source';

    public function __construct(private readonly PlannerResourceStore $resources) {}

    /** @return list<stdClass> */
    public function all(): array
    {
        return array_map($this->hydrate(...), $this->resources->all(self::KIND));
    }

    public function find(string $id): ?stdClass
    {
        $row = $this->resources->find(self::KIND, $id);

        return $row === null ? null : $this->hydrate($row);
    }

    /** @return list<string> */
    public function activeCidrs(string $origin): array
    {
        $row = $this->resources->byKey(self::KIND, $origin);

        return $row === null || ! $row->enabled ? [] : $row->document['cidrs'];
    }

    /** @param array<string, mixed> $input */
    public function create(array $input): stdClass
    {
        $this->validate($input);
        return $this->hydrate($this->resources->create(self::KIND, $input['origin'],
            ['cidrs' => $input['cidrs']], $input['enabled'] ?? true));
    }

    /** @param array<string, mixed> $input */
    public function replace(string $id, array $input, int $expectedRevision): ?stdClass
    {
        $this->validate($input);
        $row = $this->resources->replace(self::KIND, $id, $input['origin'],
            ['cidrs' => $input['cidrs']], $input['enabled'] ?? true, $expectedRevision);

        return $row === null ? null : $this->hydrate($row);
    }

    public function delete(string $id, int $expectedRevision): bool
    {
        return $this->resources->delete(self::KIND, $id, $expectedRevision);
    }

    private function hydrate(stdClass $row): stdClass
    {
        $row->origin = $row->key;
        $row->cidrs = $row->document['cidrs'];

        return $row;
    }

    /** @param array<string, mixed> $input */
    private function validate(array $input): void
    {
        if (array_diff(array_keys($input), ['origin', 'cidrs', 'enabled']) !== [] ||
            ! is_string($input['origin'] ?? null) || strlen($input['origin']) > 255 ||
            ! is_array($input['cidrs'] ?? null) || ! array_is_list($input['cidrs']) ||
            count($input['cidrs']) < 1 || count($input['cidrs']) > 16 ||
            (array_key_exists('enabled', $input) && ! is_bool($input['enabled']))) {
            throw new InvalidArgumentException('Invalid Webstream private source.');
        }
        $origin = $input['origin'];
        $parts = parse_url($origin);
        if ($parts === false || ! in_array($parts['scheme'] ?? '', ['http', 'https'], true) ||
            ! isset($parts['host'], $parts['port']) || ! is_string($parts['host']) ||
            filter_var($parts['host'], FILTER_VALIDATE_IP) !== false ||
            preg_match('/^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$/D', $parts['host']) !== 1 ||
            $parts['port'] < 1 || $parts['port'] > 65535 ||
            strtolower($parts['host']) === 'localhost' || str_ends_with(strtolower($parts['host']), '.localhost') ||
            $origin !== $parts['scheme'].'://'.strtolower($parts['host']).':'.$parts['port']) {
            throw new InvalidArgumentException('Invalid Webstream source origin.');
        }
        foreach ($input['cidrs'] as $cidr) {
            if (! is_string($cidr) || ! $this->privateCidr($cidr)) {
                throw new InvalidArgumentException('Webstream source CIDRs must be canonical private networks.');
            }
        }
    }

    private function privateCidr(string $cidr): bool
    {
        $parts = explode('/', $cidr);
        if (count($parts) !== 2 || filter_var($parts[0], FILTER_VALIDATE_IP) === false ||
            ! ctype_digit($parts[1])) {
            return false;
        }
        $address = inet_pton($parts[0]);
        $bits = (int) $parts[1];
        if ($address === false || $bits < 0 || $bits > strlen($address) * 8) {
            return false;
        }
        $mask = str_repeat("\xff", intdiv($bits, 8));
        if ($bits % 8 !== 0) {
            $mask .= chr(256 - (1 << (8 - $bits % 8)));
        }
        $mask = str_pad($mask, strlen($address), "\0");
        if (($address & $mask) !== $address || inet_ntop($address) !== $parts[0]) {
            return false;
        }
        $private = strlen($address) === 4
            ? ['10.0.0.0/8', '172.16.0.0/12', '192.168.0.0/16']
            : ['fc00::/7'];
        foreach ($private as $range) {
            [$network, $prefix] = explode('/', $range);
            $networkBytes = inet_pton($network);
            $prefix = (int) $prefix;
            $rangeMask = str_repeat("\xff", intdiv($prefix, 8));
            if ($prefix % 8 !== 0) {
                $rangeMask .= chr(256 - (1 << (8 - $prefix % 8)));
            }
            $rangeMask = str_pad($rangeMask, strlen($address), "\0");
            if ($bits >= $prefix && ($address & $rangeMask) === $networkBytes) {
                return true;
            }
        }

        return false;
    }
}
