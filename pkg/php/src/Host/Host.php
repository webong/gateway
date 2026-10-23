<?php

declare(strict_types=1);

namespace Webong\Gateway\PHP\Host;

use InvalidArgumentException;
use Webong\Gateway\Protocols\Delivery;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\Response;
use Webong\Gateway\Protocols\ScheduleCommand;
use Webong\Gateway\Protocols\Automation;
use Webong\Gateway\Protocols\AutomationCapability;
use Webong\Gateway\Protocols\AutomationExecutionTarget;

/**
 * Stateful, per-execution capability surface for PHP automation scripts.
 */
final class Host
{
    private array $scriptVariables = [];

    /** @var list<string> */
    private array $logs = [];

    /** @var list<Delivery> */
    private array $deliveries = [];

    /** @var list<ScheduleCommand> */
    private array $schedules = [];

    private ?Response $response = null;

    public function __construct(
        IngressRequest $request,
        array $variables = [],
        private readonly int $maxDeliveries = 32,
        private readonly int $maxSchedules = 8,
        private readonly int $maxLogs = 100,
    ) {
        $this->scriptVariables = array_merge($this->requestVariables($request), $variables);
    }

    /** @return array<string, callable> */
    public function functions(): array
    {
        return [
            'var' => fn (string $name, mixed $default = null): mixed => $this->variable($name, $default),
            'set' => fn (string $name, mixed $value): mixed => $this->set($name, $value),
            'respond' => fn (mixed $content = null, ?int $status = null, ?array $headers = null): mixed => $this->respond($content, $status, $headers),
            'set_response' => fn (mixed $content = null, ?int $status = null, ?array $headers = null): mixed => $this->setResponse($content, $status, $headers),
            'set_content' => fn (mixed $content): mixed => $this->setContent($content),
            'set_status' => fn (int $status): mixed => $this->setStatus($status),
            'set_header' => fn (string $name, mixed $value): mixed => $this->setHeader($name, $value),
            'echo' => fn (mixed ...$values): mixed => $this->log($values),
            'dump' => fn (mixed ...$values): mixed => $this->log($values),
            'print' => fn (mixed ...$values): mixed => $this->log($values),
            'stop' => fn (): mixed => $this->stop(),
            'json_encode' => fn (mixed $value): string => json_encode($value, JSON_THROW_ON_ERROR),
            'json_decode' => fn (string $value): mixed => json_decode($value, true, 128, JSON_THROW_ON_ERROR),
            'base64_encode' => fn (mixed $value): string => base64_encode($this->body($value)),
            'base64_decode' => fn (string $value): string => $this->decodeBase64($value),
            'hash' => fn (mixed $value, string $algorithm): string => $this->hash($value, $algorithm),
            'hmac' => fn (mixed $value, string $algorithm, string $key): string => $this->hmac($value, $algorithm, $key),
            'forward' => fn (string $url, mixed $content = null, ?string $method = null, ?array $headers = null): mixed => $this->forward($url, $content, $method, $headers),
            'schedule' => fn (array $definition): mixed => $this->schedule($definition),
        ];
    }

    public function scriptVariables(): array
    {
        return $this->scriptVariables;
    }

    public function outcome(): array
    {
        return [
            'response' => $this->response ? [
                'status' => $this->response->statusCode,
                'headers' => $this->response->headers,
                'body' => $this->response->body,
            ] : null,
            'deliveries' => array_map(fn (Delivery $d) => [
                'url' => $d->url,
                'method' => $d->method,
                'headers' => $d->headers,
                'body' => $d->body,
                'expected_status' => $d->expectedStatus,
                'expected_body_sha256' => $d->expectedBodySHA256,
            ], $this->deliveries),
            'schedules' => array_map(fn (ScheduleCommand $s) => [
                'key' => $s->key,
                'name' => $s->name,
                'cron' => $s->cron,
                'timezone' => $s->timezone,
                'language' => $s->language,
                'source' => $s->source,
                'payload' => $s->payload,
                'enabled' => $s->enabled,
            ], $this->schedules),
            'variables' => $this->scriptVariables,
            'logs' => $this->logs,
        ];
    }

    private function variable(string $name, mixed $default = null): mixed
    {
        $path = trim($name, '$');
        return $this->scriptVariables[$path] ?? $default;
    }

    private function set(string $name, mixed $value): void
    {
        $path = trim($name, '$');
        $this->scriptVariables[$path] = $value;
    }

    private function respond(mixed $content = null, ?int $status = null, ?array $headers = null): never
    {
        $this->response = $this->makeResponse($content, $status, $headers);
        throw new RespondException;
    }

    private function setResponse(mixed $content = null, ?int $status = null, ?array $headers = null): void
    {
        $this->response = $this->makeResponse($content, $status, $headers);
    }

    private function setContent(mixed $content): void
    {
        $this->response = new Response(
            statusCode: $this->response?->statusCode ?? 200,
            headers: $this->response?->headers ?? [],
            body: $this->body($content),
        );
    }

    private function setStatus(int $status): void
    {
        if ($status < 100 || $status > 599) {
            throw new InvalidArgumentException('PHP response status is outside the HTTP range.');
        }
        $this->response = new Response(
            statusCode: $status,
            headers: $this->response?->headers ?? [],
            body: $this->response?->body ?? '',
        );
    }

    private function setHeader(string $name, mixed $value): void
    {
        $headers = $this->response?->headers ?? [];
        $headers[$name] = [(string) $this->scalar($value)];
        $this->response = new Response(
            statusCode: $this->response?->statusCode ?? 200,
            headers: $headers,
            body: $this->response?->body ?? '',
        );
    }

    /** @param list<mixed> $values */
    private function log(array $values): void
    {
        if (count($this->logs) >= $this->maxLogs) {
            throw new InvalidArgumentException('PHP automation exceeded the debug log limit.');
        }

        $message = implode(' ', array_map(
            fn (mixed $value): string => is_scalar($php = $this->toPhp($value)) || $php === null
                ? (string) $php
                : json_encode($php, JSON_THROW_ON_ERROR),
            $values,
        ));
        $this->logs[] = mb_substr($message, 0, 4096);
    }

    private function stop(): never
    {
        throw new RespondException;
    }

    private function hash(mixed $value, string $algorithm): string
    {
        $algorithm = strtolower($algorithm);
        if (! in_array($algorithm, ['md5', 'sha1', 'sha256', 'sha384', 'sha512'], true)) {
            throw new InvalidArgumentException('PHP automation hash algorithm is not allowed.');
        }

        return hash($algorithm, $this->body($value));
    }

    private function hmac(mixed $value, string $algorithm, string $key): string
    {
        $algorithm = strtolower($algorithm);
        if (! in_array($algorithm, ['md5', 'sha1', 'sha256', 'sha384', 'sha512'], true)) {
            throw new InvalidArgumentException('PHP automation HMAC algorithm is not allowed.');
        }

        return hash_hmac($algorithm, $this->body($value), $key);
    }

    private function forward(string $url, mixed $content = null, ?string $method = null, ?array $headers = null): void
    {
        if (count($this->deliveries) >= $this->maxDeliveries) {
            throw new InvalidArgumentException('PHP automation exceeded the delivery limit.');
        }

        $this->deliveries[] = new Delivery(
            url: $url,
            method: strtoupper($method ?? 'POST'),
            headers: $this->headers($headers),
            body: $content === null ? '' : $this->body($content),
        );
    }

    private function schedule(array $definition): void
    {
        if (count($this->schedules) >= $this->maxSchedules) {
            throw new InvalidArgumentException('PHP automation exceeded the schedule limit.');
        }

        $this->schedules[] = ScheduleCommand::fromArray($definition);
    }

    private function makeResponse(mixed $content = null, ?int $status = null, ?array $headers = null): Response
    {
        return new Response(
            statusCode: $status ?? 200,
            headers: $this->headers($headers),
            body: $content === null ? '' : $this->body($content),
        );
    }

    /** @return array<string, list<string>> */
    private function headers(?array $headers): array
    {
        if ($headers === null) {
            return [];
        }

        $normalized = [];
        foreach ($headers as $key => $value) {
            if (is_int($key)) {
                $line = (string) $this->scalar($value);
                [$name, $headerValue] = array_pad(explode(':', $line, 2), 2, null);
                if ($headerValue === null || trim($name) === '') {
                    throw new InvalidArgumentException('PHP automation response header is invalid.');
                }
                $normalized[trim($name)][] = trim($headerValue);
                continue;
            }

            $php = $this->toPhp($value);
            $normalized[(string) $key] = is_array($php)
                ? array_values(array_map(static fn (mixed $item): string => (string) $item, $php))
                : [(string) $php];
        }

        return $normalized;
    }

    private function body(mixed $value): string
    {
        $value = $this->toPhp($value);

        return is_array($value)
            ? json_encode($value, JSON_THROW_ON_ERROR)
            : (string) $value;
    }

    private function scalar(mixed $value): string|int|float|bool|null
    {
        $value = $this->toPhp($value);
        if (! is_scalar($value) && $value !== null) {
            throw new InvalidArgumentException('PHP automation value must be scalar.');
        }

        return $value;
    }

    private function toPhp(mixed $value): mixed
    {
        if ($value === null) {
            return null;
        }
        if (is_scalar($value)) {
            return $value;
        }
        if (is_array($value)) {
            return array_map(fn ($item) => $this->toPhp($item), $value);
        }
        return (string) $value;
    }

    private function decodeBase64(string $value): string
    {
        $decoded = base64_decode($value, true);
        if ($decoded === false) {
            throw new InvalidArgumentException('PHP automation base64 value is invalid.');
        }
        return $decoded;
    }

    /** @return array<string, mixed> */
    private function requestVariables(IngressRequest $request): array
    {
        parse_str($request->rawQuery, $query);
        $variables = [
            'request.uuid' => $request->deliveryId,
            'request.method' => $request->method,
            'request.scheme' => $request->scheme,
            'request.hostname' => $request->host,
            'request.host' => $request->host,
            'request.path' => $request->path,
            'request.query' => $request->rawQuery,
            'request.content' => $request->body,
            'request.size' => strlen($request->body),
            'request.type' => 'web',
        ];

        foreach ($query as $name => $value) {
            $variables['request.query.'.strtolower((string) $name)] = $value;
        }

        foreach ($request->headers as $name => $values) {
            $values = array_values($values);
            $variables['request.header.'.strtolower($name)] = count($values) === 1 ? $values[0] : $values;
        }

        $url = $request->scheme.'://'.$request->host.$request->path;
        if ($request->rawQuery !== '') {
            $url .= '?'.$request->rawQuery;
        }

        $variables['request.url'] = $url;
        $variables['request.user_agent'] = $variables['request.header.user-agent'] ?? '';

        return $variables;
    }
}

/** @internal */
final class RespondException extends \Exception
{
}