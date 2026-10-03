<?php

declare(strict_types=1);

namespace Webong\Gateway\Deliveries;

use Illuminate\Http\Client\ConnectionException;
use Illuminate\Http\Client\Response;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Http;
use JsonException;
use RuntimeException;
use Throwable;

final class DeliveryAdminClient
{
    /** @param array<string, string|int> $query */
    public function get(string $path, array $query = [], ?string $node = null): Response
    {
        return $this->request('GET', $path, $query, $node);
    }

    public function post(string $path, ?string $node = null): Response
    {
        return $this->request('POST', $path, [], $node);
    }

    /** @param array<string, mixed> $payload */
    public function postJson(string $path, array $payload, ?string $node = null): Response
    {
        return $this->request('POST', $path, [], $node, $payload);
    }

    /** @return array<string, string> */
    public function nodes(): array
    {
        $token = (string) config('gateway.internal_token', '');
        if ($token === '') {
            throw new RuntimeException('The delivery admin connection is not configured.');
        }
        if (($shared = $this->sharedOrigins()) !== null) {
            if (config('gateway.delivery_admin_nodes') !== null) {
                throw new RuntimeException('Shared delivery admin origins cannot be combined with local-spool nodes.');
            }

            return ['shared' => $shared[0]];
        }
        $configured = config('gateway.delivery_admin_nodes');
        if ($configured === null) {
            $configured = ['local' => config('gateway.delivery_admin_url', '')];
        } elseif (is_string($configured)) {
            try {
                $configured = json_decode($configured, true, 512, JSON_THROW_ON_ERROR);
            } catch (JsonException) {
                throw new RuntimeException('The delivery admin nodes are invalid.');
            }
        }
        if (! is_array($configured) || $configured === [] || array_is_list($configured) || count($configured) > 64) {
            throw new RuntimeException('The delivery admin nodes are invalid.');
        }
        $nodes = [];
        foreach ($configured as $name => $url) {
            if (! is_string($name) || preg_match('/\A[a-zA-Z0-9_-]{1,64}\z/D', $name) !== 1
                || ! is_string($url) || ! $this->validOrigin($url)) {
                throw new RuntimeException('The delivery admin nodes are invalid.');
            }
            $nodes[$name] = rtrim($url, '/');
        }

        return $nodes;
    }

    /** @return list<string>|null */
    private function sharedOrigins(): ?array
    {
        if (config('gateway.delivery_admin_discovery_enabled', false)) {
            if (config('gateway.delivery_admin_shared_urls') !== null || config('gateway.delivery_admin_nodes') !== null) {
                throw new RuntimeException('Delivery admin discovery cannot be combined with configured nodes or shared origins.');
            }

            return $this->discoveredOrigins();
        }
        $configured = config('gateway.delivery_admin_shared_urls');
        if ($configured === null || $configured === '') {
            return null;
        }
        if (is_string($configured)) {
            try {
                $configured = json_decode($configured, true, 512, JSON_THROW_ON_ERROR);
            } catch (JsonException) {
                throw new RuntimeException('The shared delivery admin origins are invalid.');
            }
        }
        if (! is_array($configured) || ! array_is_list($configured) || count($configured) < 2 || count($configured) > 16) {
            throw new RuntimeException('The shared delivery admin origins are invalid.');
        }
        $origins = [];
        foreach ($configured as $url) {
            if (! is_string($url) || ! $this->validOrigin($url)) {
                throw new RuntimeException('The shared delivery admin origins are invalid.');
            }
            $origin = rtrim($url, '/');
            if (in_array($origin, $origins, true)) {
                throw new RuntimeException('The shared delivery admin origins are invalid.');
            }
            $origins[] = $origin;
        }

        return $origins;
    }

    /** @return list<string> */
    private function discoveredOrigins(): array
    {
        try {
            $rows = DB::table('delivery_admin_endpoints')
                ->where('expires_unix', '>', now()->getTimestamp())
                ->orderBy('id')
                ->limit(64)
                ->get(['id', 'origin', 'expires_unix', 'signature']);
        } catch (Throwable $exception) {
            throw new RuntimeException('Delivery admin discovery is unavailable.', previous: $exception);
        }
        $token = (string) config('gateway.internal_token', '');
        $origins = [];
        foreach ($rows as $row) {
            if (! is_string($row->id) || preg_match('/\A[0-9a-f]{32}\z/D', $row->id) !== 1
                || ! is_string($row->origin) || ! $this->validOrigin($row->origin)
                || ! ctype_digit((string) $row->expires_unix)
                || ! is_string($row->signature) || preg_match('/\A[0-9a-f]{64}\z/D', $row->signature) !== 1
                || ! hash_equals(hash_hmac('sha256', $row->id."\n".$row->origin."\n".$row->expires_unix, $token), $row->signature)) {
                throw new RuntimeException('Delivery admin discovery contains an invalid endpoint.');
            }
            $origin = rtrim($row->origin, '/');
            if (! in_array($origin, $origins, true)) {
                $origins[] = $origin;
            }
        }
        if ($origins === []) {
            throw new RuntimeException('No live delivery admin endpoints are registered.');
        }

        return $origins;
    }

    private function validOrigin(string $url): bool
    {
        $parts = parse_url($url);

        return is_array($parts)
            && in_array($parts['scheme'] ?? null, ['http', 'https'], true)
            && isset($parts['host']) && $parts['host'] !== ''
            && ! isset($parts['user']) && ! isset($parts['pass'])
            && ! isset($parts['query']) && ! isset($parts['fragment'])
            && in_array($parts['path'] ?? '', ['', '/'], true);
    }

    /** @param array<string, string|int> $query */
    private function request(string $method, string $path, array $query = [], ?string $node = null, ?array $payload = null): Response
    {
        $nodes = $this->nodes();
        $selected = $node ?? array_key_first($nodes);
        if (! isset($nodes[$selected])) {
            throw new RuntimeException('The delivery admin node is not configured.');
        }
        if ($selected === 'shared' && ($origins = $this->sharedOrigins()) !== null) {
            return $this->requestShared($method, $path, $query, $payload, $origins);
        }

        return $this->send($method, $nodes[$selected], $path, $query, $payload);
    }

    /**
     * @param array<string, string|int> $query
     * @param list<string> $origins
     */
    private function requestShared(string $method, string $path, array $query, ?array $payload, array $origins): Response
    {
        if ($method === 'POST' && preg_match('/\A\/deliveries\/[0-9a-f]{32}\/retry\z/D', $path) === 1) {
            $statusPath = substr($path, 0, -strlen('/retry'));
            foreach ($origins as $origin) {
                try {
                    $status = $this->send('GET', $origin, $statusPath, []);
                } catch (ConnectionException) {
                    continue;
                }
                if ($status->serverError()) {
                    continue;
                }
                if (! in_array($status->status(), [200, 404], true)) {
                    return $status;
                }
                if ($status->status() === 200 && $status->json('id') !== substr($statusPath, strlen('/deliveries/'))) {
                    throw new RuntimeException('The shared delivery admin origin returned the wrong delivery.');
                }

                // Send the mutation once. A lost POST response is ambiguous,
                // so never replay it on another origin.
                return $this->send('POST', $origin, $path);
            }

            throw new RuntimeException('No shared delivery admin origin is available.');
        }

        $replayable = $method === 'GET' || ($method === 'POST' && $path === '/email/submissions');
        foreach ($origins as $index => $origin) {
            try {
                $response = $this->send($method, $origin, $path, $query, $payload);
            } catch (ConnectionException $exception) {
                if (! $replayable || $index === array_key_last($origins)) {
                    throw $exception;
                }

                continue;
            }
            if (! $replayable || ! $response->serverError() || $index === array_key_last($origins)) {
                return $response;
            }
        }

        throw new RuntimeException('No shared delivery admin origin is available.');
    }

    /** @param array<string, string|int> $query */
    private function send(string $method, string $base, string $path, array $query = [], ?array $payload = null): Response
    {
        $token = (string) config('gateway.internal_token', '');

        $request = Http::withToken($token)
            ->acceptJson()
            ->withoutRedirecting()
            ->connectTimeout(2)
            ->timeout($payload === null ? 5 : 30);

        $url = $base.$path;

        return $method === 'POST'
            ? $request->send('POST', $url, $payload === null ? [] : ['json' => $payload])
            : $request->get($url, $query);
    }
}
