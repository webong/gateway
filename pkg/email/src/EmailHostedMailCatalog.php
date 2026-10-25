<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use InvalidArgumentException;
use stdClass;
use Webong\Gateway\Resources\PlannerResourceStore;

/** Hosted HTTP adapters and exact-recipient routes in the shared CMS store. */
final readonly class EmailHostedMailCatalog
{
    private const ADAPTER = 'email.hosted-adapter';
    private const ROUTE = 'email.hosted-route';

    public function __construct(private PlannerResourceStore $resources) {}

    public function allAdapters(): array { return $this->resources->all(self::ADAPTER); }
    public function allRoutes(): array { return $this->resources->all(self::ROUTE); }
    public function findAdapter(string $id): ?stdClass { return $this->resources->find(self::ADAPTER, $id); }
    public function findRoute(string $id): ?stdClass { return $this->resources->find(self::ROUTE, $id); }

    /** @return array{url: string, bearer_token: string}|null */
    public function adapter(string $name): ?array
    {
        $row = $this->resources->byKey(self::ADAPTER, $name);

        return $row !== null && $row->enabled && $this->allowed($row->document['url']) ? $row->document : null;
    }

    public function route(string $recipient): ?EmailProviderRoute
    {
        $row = $this->resources->byKey(self::ROUTE, strtolower($recipient));
        if ($row === null || ! $row->enabled || $this->adapter($row->document['adapter']) === null) {
            return null;
        }

        return new EmailProviderRoute($row->document['account_id'], 'hosted.mail',
            $row->document['mailbox_key'], $row->document['adapter']);
    }

    public function createAdapter(array $input): stdClass
    {
        $this->validateAdapter($input);

        return $this->resources->create(self::ADAPTER, $input['name'],
            ['url' => $input['url'], 'bearer_token' => $input['bearer_token']], $input['enabled'] ?? true);
    }

    public function replaceAdapter(string $id, array $input, int $revision): ?stdClass
    {
        $current = $this->findAdapter($id);
        if ($current === null) { return null; }
        $input['bearer_token'] ??= $current->document['bearer_token'];
        $this->validateAdapter($input);

        return $this->resources->replace(self::ADAPTER, $id, $input['name'],
            ['url' => $input['url'], 'bearer_token' => $input['bearer_token']],
            $input['enabled'] ?? $current->enabled, $revision);
    }

    public function deleteAdapter(string $id, int $revision): bool
    {
        return $this->resources->delete(self::ADAPTER, $id, $revision);
    }

    public function createRoute(array $input): stdClass
    {
        $this->validateRoute($input);

        return $this->resources->create(self::ROUTE, strtolower($input['recipient']), $this->routeDocument($input),
            $input['enabled'] ?? true);
    }

    public function replaceRoute(string $id, array $input, int $revision): ?stdClass
    {
        $this->validateRoute($input);

        return $this->resources->replace(self::ROUTE, $id, strtolower($input['recipient']),
            $this->routeDocument($input), $input['enabled'] ?? true, $revision);
    }

    public function deleteRoute(string $id, int $revision): bool
    {
        return $this->resources->delete(self::ROUTE, $id, $revision);
    }

    private function routeDocument(array $input): array
    {
        return ['account_id' => $input['account_id'], 'adapter' => $input['adapter'],
            'mailbox_key' => $input['mailbox_key']];
    }

    private function validateAdapter(array $input): void
    {
        if (array_diff(array_keys($input), ['name', 'url', 'bearer_token', 'enabled']) !== [] ||
            ! is_string($input['name'] ?? null) || preg_match('/^[a-z][a-z0-9._-]{0,63}$/D', $input['name']) !== 1 ||
            ! str_contains($input['name'], '.') || ! is_string($input['url'] ?? null) ||
            ! $this->allowed($input['url']) || ! is_string($input['bearer_token'] ?? null) ||
            $input['bearer_token'] === '' || strlen($input['bearer_token']) > 4096 ||
            preg_match('/[^\x21-\x7E]/', $input['bearer_token']) === 1 ||
            (array_key_exists('enabled', $input) && ! is_bool($input['enabled']))) {
            throw new InvalidArgumentException('Hosted mail adapter is invalid or outside the deployed host ceiling.');
        }
    }

    private function validateRoute(array $input): void
    {
        if (array_diff(array_keys($input), ['recipient', 'account_id', 'adapter', 'mailbox_key', 'enabled']) !== [] ||
            ! is_string($input['recipient'] ?? null) || ! filter_var($input['recipient'], FILTER_VALIDATE_EMAIL) ||
            strlen($input['recipient']) > 320 || ! is_string($input['adapter'] ?? null) ||
            $this->adapter($input['adapter']) === null ||
            ! is_string($input['account_id'] ?? null) || $input['account_id'] === '' ||
            strlen($input['account_id']) > 255 || preg_match('/[\x00-\x1F\x7F]/', $input['account_id']) === 1 ||
            ! is_string($input['mailbox_key'] ?? null) || $input['mailbox_key'] === '' ||
            strlen($input['mailbox_key']) > 320 || preg_match('/[\x00-\x1F\x7F]/', $input['mailbox_key']) === 1 ||
            (array_key_exists('enabled', $input) && ! is_bool($input['enabled']))) {
            throw new InvalidArgumentException('Hosted mail route is invalid.');
        }
    }

    private function allowed(string $url): bool
    {
        $parts = parse_url($url);
        if (! is_array($parts) || ($parts['scheme'] ?? null) !== 'https' ||
            ! is_string($parts['host'] ?? null) || $parts['host'] !== strtolower($parts['host']) ||
            isset($parts['user']) || isset($parts['pass']) || isset($parts['query']) || isset($parts['fragment']) ||
            strlen($url) > 2048) {
            return false;
        }
        $hosts = config('gateway.email.hosted_allowed_hosts', []);

        return is_array($hosts) && in_array($parts['host'], $hosts, true);
    }
}
