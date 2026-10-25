<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use InvalidArgumentException;
use stdClass;
use Webong\Gateway\Resources\PlannerResourceStore;

/** Planner-owned forwarding policies for upstream DNS resolvers. */
final readonly class DnsResolverStore
{
    private const KIND = 'dns.resolver';

    public function __construct(private PlannerResourceStore $resources) {}

    /** @return list<stdClass> */
    public function all(): array
    {
        return array_map($this->hydrate(...), $this->resources->all(self::KIND));
    }

    /** @return list<array<string, mixed>> */
    public function active(): array
    {
        return array_values(array_map(
            static fn (stdClass $resolver): array => [
                'name' => $resolver->name,
                ...$resolver->document,
            ],
            array_filter($this->all(), static fn (stdClass $resolver): bool => $resolver->enabled),
        ));
    }

    public function find(string $id): ?stdClass
    {
        $row = $this->resources->find(self::KIND, $id);

        return $row === null ? null : $this->hydrate($row);
    }

    public function findByName(string $name): ?stdClass
    {
        $row = $this->resources->byKey(self::KIND, $name);

        return $row === null ? null : $this->hydrate($row);
    }

    /** @param array<string, mixed> $input */
    public function create(array $input): stdClass
    {
        if (count($this->all()) >= 32) {
            throw new InvalidArgumentException('DNS forwarded resolver count exceeds 32.');
        }
        $normalized = $this->normalize($input);
        $this->assertRoutesAvailable($normalized['domains'], $normalized['client_cidrs']);

        return $this->hydrate($this->resources->create(
            self::KIND,
            $normalized['name'],
            $this->document($normalized),
            $normalized['enabled'],
        ));
    }

    /** @param array<string, mixed> $input */
    public function replace(string $id, array $input, int $revision): ?stdClass
    {
        $normalized = $this->normalize($input);
        $this->assertRoutesAvailable($normalized['domains'], $normalized['client_cidrs'], $id);
        $row = $this->resources->replace(
            self::KIND,
            $id,
            $normalized['name'],
            $this->document($normalized),
            $normalized['enabled'],
            $revision,
        );

        return $row === null ? null : $this->hydrate($row);
    }

    public function delete(string $id, int $revision): bool
    {
        return $this->resources->delete(self::KIND, $id, $revision);
    }

    private function hydrate(stdClass $row): stdClass
    {
        $row->name = $row->key;

        return $row;
    }

    /** @param array<string, mixed> $normalized
     *  @return array<string, mixed>
     */
    private function document(array $normalized): array
    {
        return [
            'address' => $normalized['address'],
            'transport' => $normalized['transport'],
            'domains' => $normalized['domains'],
            'client_cidrs' => $normalized['client_cidrs'],
            'timeout_ms' => $normalized['timeout_ms'],
        ];
    }

    /** @param array<string, mixed> $input
     *  @return array{name: string, address: string, transport: string, domains: list<string>, client_cidrs: list<string>, timeout_ms: int, enabled: bool}
     */
    private function normalize(array $input): array
    {
        if (array_diff(array_keys($input), ['name', 'address', 'transport', 'domains', 'client_cidrs', 'timeout_ms', 'enabled']) !== []
            || ! is_string($input['name'] ?? null)
            || preg_match('/^[a-z][a-z0-9._-]{0,63}$/D', $input['name']) !== 1
            || ! is_string($input['address'] ?? null)
            || ! in_array($input['transport'] ?? 'udp', ['udp', 'tcp'], true)
            || ! is_array($input['domains'] ?? null) || ! array_is_list($input['domains'])
            || count($input['domains']) < 1 || count($input['domains']) > 32
            || ! is_array($input['client_cidrs'] ?? null) || ! array_is_list($input['client_cidrs'])
            || count($input['client_cidrs']) < 1 || count($input['client_cidrs']) > 32
            || ! is_int($input['timeout_ms'] ?? 2000)
            || ($input['timeout_ms'] ?? 2000) < 10 || ($input['timeout_ms'] ?? 2000) > 5000
            || (array_key_exists('enabled', $input) && ! is_bool($input['enabled']))) {
            throw new InvalidArgumentException('Invalid DNS forwarded resolver.');
        }
        $address = $this->normalizeAddress($input['address']);
        $domains = array_map($this->normalizeDomain(...), $input['domains']);
        $cidrs = array_map($this->normalizeCidr(...), $input['client_cidrs']);
        if (count(array_unique($domains)) !== count($domains) || count(array_unique($cidrs)) !== count($cidrs)) {
            throw new InvalidArgumentException('DNS forwarded resolver routes must be unique.');
        }

        return [
            'name' => $input['name'],
            'address' => $address,
            'transport' => $input['transport'] ?? 'udp',
            'domains' => $domains,
            'client_cidrs' => $cidrs,
            'timeout_ms' => $input['timeout_ms'] ?? 2000,
            'enabled' => $input['enabled'] ?? true,
        ];
    }

    /** @param list<string> $domains
     *  @param list<string> $clientCidrs
     */
    private function assertRoutesAvailable(array $domains, array $clientCidrs, ?string $except = null): void
    {
        foreach ($this->all() as $resolver) {
            if ($resolver->id === $except) {
                continue;
            }
            if (array_intersect($domains, $resolver->document['domains']) !== []
                && array_intersect($clientCidrs, $resolver->document['client_cidrs']) !== []) {
                throw new InvalidArgumentException('DNS forwarded resolver route already exists.');
            }
        }
    }

    private function normalizeAddress(string $address): string
    {
        if (preg_match('/^\[([^]]+)]\:([0-9]{1,5})$/D', $address, $matches) === 1) {
            $packed = filter_var($matches[1], FILTER_VALIDATE_IP, FILTER_FLAG_IPV6) !== false
                ? inet_pton($matches[1]) : false;
            $port = (int) $matches[2];
            if ($packed === false || $port < 1 || $port > 65535) {
                throw new InvalidArgumentException('Invalid DNS forwarded resolver address.');
            }

            return '['.inet_ntop($packed).']:'.$port;
        }
        if (substr_count($address, ':') !== 1) {
            throw new InvalidArgumentException('Invalid DNS forwarded resolver address.');
        }
        [$host, $rawPort] = explode(':', $address, 2);
        $host = strtolower(rtrim($host, '.'));
        $port = ctype_digit($rawPort) ? (int) $rawPort : 0;
        if ($host === '' || $port < 1 || $port > 65535
            || filter_var($host, FILTER_VALIDATE_IP, FILTER_FLAG_IPV4) === false
                && preg_match('/^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$/D', $host) !== 1) {
            throw new InvalidArgumentException('Invalid DNS forwarded resolver address.');
        }

        return $host.':'.$port;
    }

    private function normalizeDomain(mixed $domain): string
    {
        if (! is_string($domain)) {
            throw new InvalidArgumentException('Invalid DNS forwarded resolver domain.');
        }
        $domain = strtolower(rtrim(trim($domain), '.'));
        if ($domain === '') {
            return '.';
        }
        if (strlen($domain) > 253) {
            throw new InvalidArgumentException('Invalid DNS forwarded resolver domain.');
        }
        foreach (explode('.', $domain) as $label) {
            if ($label === '' || strlen($label) > 63
                || preg_match('/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/D', $label) !== 1) {
                throw new InvalidArgumentException('Invalid DNS forwarded resolver domain.');
            }
        }

        return $domain;
    }

    private function normalizeCidr(mixed $cidr): string
    {
        if (! is_string($cidr) || substr_count($cidr, '/') !== 1) {
            throw new InvalidArgumentException('Invalid DNS forwarded resolver client network.');
        }
        [$ip, $rawBits] = explode('/', $cidr, 2);
        $packed = inet_pton($ip);
        if ($packed === false || ! ctype_digit($rawBits)) {
            throw new InvalidArgumentException('Invalid DNS forwarded resolver client network.');
        }
        $bits = (int) $rawBits;
        $maximum = strlen($packed) * 8;
        if ($bits < 0 || $bits > $maximum || trim($packed, "\0") === '') {
            throw new InvalidArgumentException('Invalid DNS forwarded resolver client network.');
        }
        $mask = str_repeat("\xff", intdiv($bits, 8));
        if ($bits % 8 !== 0) {
            $mask .= chr(256 - (1 << (8 - $bits % 8)));
        }
        $mask = str_pad($mask, strlen($packed), "\0");
        if (($packed & $mask) !== $packed) {
            throw new InvalidArgumentException('DNS forwarded resolver client networks must be canonical.');
        }

        return strtolower((string) inet_ntop($packed)).'/'.$bits;
    }
}
