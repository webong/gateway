<?php

declare(strict_types=1);

namespace Webong\Gateway\HookScript;

use InvalidArgumentException;
use Webong\Gateway\Contracts\CompanionAction;
use Webong\Gateway\Protocols\CompanionActionInvocation;
use Webong\Gateway\Protocols\CompanionActionResult;
use Webong\Gateway\Protocols\IngressRequest;

final readonly class HookScriptCompanionAction implements CompanionAction
{
    public const string NAME = 'gateway.hookscript.execute';

    public const string VERSION = '1.0.0';

    public function __construct(private HookScriptRuntime $runtime) {}

    public function handle(CompanionActionInvocation $invocation): CompanionActionResult
    {
        try {
            $input = $this->input($invocation->input);
            $outcome = $this->runtime->execute(
                $input['source'],
                $this->request($invocation, $input['event']),
                $input['variables'],
            );
        } catch (InvalidArgumentException $exception) {
            return CompanionActionResult::failed(
                $invocation->executionId,
                'hookscript_invalid_input',
                $exception->getMessage(),
            );
        } catch (HookScriptExecutionException $exception) {
            return CompanionActionResult::failed(
                $invocation->executionId,
                'hookscript_execution_failed',
                $exception->getMessage(),
            );
        }

        return CompanionActionResult::succeeded($invocation->executionId, [
            'outcome' => [
                'variables' => $outcome->variables === [] ? (object) [] : $outcome->variables,
                'deliveries' => array_map(static fn ($delivery): array => [
                    'url' => $delivery->url,
                    'method' => $delivery->method,
                    'headers' => $delivery->headers === [] ? (object) [] : $delivery->headers,
                    'body' => $delivery->body,
                ], $outcome->deliveries),
                'schedules' => array_map(static fn ($schedule): array => [
                    'key' => $schedule->key,
                    'name' => $schedule->name,
                    'cron' => $schedule->cron,
                    'timezone' => $schedule->timezone,
                    'language' => $schedule->language,
                    'source' => $schedule->source,
                    'payload' => $schedule->payload === [] ? (object) [] : $schedule->payload,
                    'enabled' => $schedule->enabled,
                ], $outcome->schedules),
                'response' => $outcome->response === null ? null : [
                    'status' => $outcome->response->statusCode,
                    'headers' => $outcome->response->headers === [] ? (object) [] : $outcome->response->headers,
                    'body' => $outcome->response->body,
                ],
            ],
        ]);
    }

    /**
     * @param array<string, mixed> $input
     * @return array{source: string, event: array<string, mixed>, variables: array<string, mixed>}
     */
    private function input(array $input): array
    {
        if (array_diff(array_keys($input), ['source', 'event', 'variables']) !== []) {
            throw new InvalidArgumentException('HookScript companion input contains unsupported fields.');
        }
        if (! is_string($input['source'] ?? null)) {
            throw new InvalidArgumentException('HookScript companion source must be a string.');
        }
        $event = $input['event'] ?? null;
        $variables = $input['variables'] ?? null;
        if (! is_array($event) || array_is_list($event) && $event !== []) {
            throw new InvalidArgumentException('HookScript companion event must be an object.');
        }
        if (! is_array($variables) || array_is_list($variables) && $variables !== []) {
            throw new InvalidArgumentException('HookScript companion variables must be an object.');
        }

        return ['source' => $input['source'], 'event' => $event, 'variables' => $variables];
    }

    /** @param array<string, mixed> $event */
    private function request(CompanionActionInvocation $invocation, array $event): IngressRequest
    {
        return new IngressRequest(
            deliveryId: $this->string($event, 'delivery_id', $invocation->executionId),
            method: strtoupper($this->string($event, 'method', 'POST')),
            host: $this->string($event, 'host', 'gateway.internal'),
            path: $this->string($event, 'path', '/automation/hookscript'),
            rawQuery: $this->string($event, 'raw_query'),
            headers: $this->headers($event['headers'] ?? []),
            body: $this->body($event['body'] ?? ''),
            scheme: $this->string($event, 'scheme', 'https'),
            protocol: $this->string($event, 'protocol', 'http'),
            event: $this->string($event, 'type', $this->string($event, 'event', 'request')),
        );
    }

    /** @return array<string, list<string>> */
    private function headers(mixed $headers): array
    {
        if (! is_array($headers)) {
            throw new InvalidArgumentException('HookScript companion headers must be an object.');
        }

        $normalized = [];
        foreach ($headers as $name => $values) {
            if (! is_string($name) || ! is_array($values)) {
                throw new InvalidArgumentException('HookScript companion headers must be a string-to-list map.');
            }
            $normalized[$name] = [];
            foreach ($values as $value) {
                if (! is_scalar($value)) {
                    throw new InvalidArgumentException('HookScript companion header values must be strings.');
                }
                $normalized[$name][] = (string) $value;
            }
        }

        return $normalized;
    }

    private function body(mixed $body): string
    {
        if (is_string($body)) {
            return $body;
        }
        if (is_scalar($body) || $body === null) {
            return (string) $body;
        }

        return json_encode($body, JSON_THROW_ON_ERROR);
    }

    /** @param array<string, mixed> $values */
    private function string(array $values, string $key, string $default = ''): string
    {
        $value = $values[$key] ?? $default;

        return is_scalar($value) ? (string) $value : $default;
    }
}
