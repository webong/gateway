<?php

declare(strict_types=1);

namespace Webong\Gateway\Webstream;

use InvalidArgumentException;
use stdClass;
use Webong\Gateway\Resources\PlannerResourceStore;

/** Exact ingress route => exact approved stream source and upload/retry switches. */
final class WebstreamRoutePolicyStore
{
    private const KIND = 'webstream.route-policy';

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

    public function forRoute(string $host, string $path): ?stdClass
    {
        $row = $this->resources->byKey(self::KIND, $this->routeKey($host, $path));

        return $row === null ? null : $this->hydrate($row);
    }

    /** @param array<string, mixed> $input */
    public function create(array $input): stdClass
    {
        $this->validate($input);
        return $this->hydrate($this->resources->create(self::KIND,
            $this->routeKey($input['host'], $input['path']), $this->document($input), $input['enabled'] ?? true));
    }

    /** @param array<string, mixed> $input */
    public function replace(string $id, array $input, int $expectedRevision): ?stdClass
    {
        $this->validate($input);
        $row = $this->resources->replace(self::KIND, $id,
            $this->routeKey($input['host'], $input['path']), $this->document($input),
            $input['enabled'] ?? true, $expectedRevision);

        return $row === null ? null : $this->hydrate($row);
    }

    public function delete(string $id, int $expectedRevision): bool
    {
        return $this->resources->delete(self::KIND, $id, $expectedRevision);
    }

    /** @param array<string, mixed> $input
     * @return array<string, mixed>
     */
    private function document(array $input): array
    {
        return ['host' => strtolower($input['host']), 'path' => $input['path'], 'source' => $input['source'],
            'upload' => $input['upload'] ?? false, 'retry' => $input['retry'] ?? false];
    }

    private function hydrate(stdClass $row): stdClass
    {
        foreach ($row->document as $name => $value) {
            $row->{$name} = $value;
        }

        return $row;
    }

    private function routeKey(string $host, string $path): string
    {
        return strtolower($host)."\n".$path;
    }

    /** @param array<string, mixed> $input */
    private function validate(array $input): void
    {
        if (array_diff(array_keys($input), ['host', 'path', 'source', 'upload', 'retry', 'enabled']) !== [] ||
            ! is_string($input['host'] ?? null) || strlen($input['host']) > 255 ||
            preg_match('/^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?(?::[1-9][0-9]{0,4})?$/Di', $input['host']) !== 1 ||
            ! is_string($input['path'] ?? null) || strlen($input['path']) > 2048 ||
            preg_match('~^/(?:[^?#\x00-\x1f]*)$~D', $input['path']) !== 1 ||
            ! is_string($input['source'] ?? null) || strlen($input['source']) > 4096 ||
            (array_key_exists('upload', $input) && ! is_bool($input['upload'])) ||
            (array_key_exists('retry', $input) && ! is_bool($input['retry'])) ||
            (array_key_exists('enabled', $input) && ! is_bool($input['enabled']))) {
            throw new InvalidArgumentException('Invalid Webstream route policy.');
        }
        $source = parse_url($input['source']);
        if ($source === false || ! in_array($source['scheme'] ?? '', ['http', 'https'], true) ||
            ! isset($source['host']) || isset($source['user']) || isset($source['pass']) ||
            isset($source['fragment'])) {
            throw new InvalidArgumentException('Invalid Webstream route source.');
        }
    }
}
