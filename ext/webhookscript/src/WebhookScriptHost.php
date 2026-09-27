<?php

declare(strict_types=1);

namespace Webong\Gateway\WebhookScript;

use InvalidArgumentException;
use Smuuf\Primi\Structures\ArrayValue;
use Smuuf\Primi\Structures\BoolValue;
use Smuuf\Primi\Structures\NullValue;
use Smuuf\Primi\Structures\NumberValue;
use Smuuf\Primi\Structures\RegexValue;
use Smuuf\Primi\Structures\StringValue;
use Smuuf\Primi\Structures\Value;
use Webong\Gateway\Protocol\Delivery;
use Webong\Gateway\Protocol\IngressRequest;
use Webong\Gateway\Protocol\Response;

/** @internal Stateful, per-execution capability surface for Primi. */
final class WebhookScriptHost
{
    private ArrayValue $scriptVariables;

    /** @var list<string> */
    private array $logs = [];

    /** @var list<Delivery> */
    private array $deliveries = [];

    private ?Response $response = null;

    public function __construct(
        IngressRequest $request,
        private readonly int $maxDeliveries,
        private readonly int $maxLogs,
    ) {
        $scriptVariables = Value::buildAutomatic($this->requestVariables($request));
        if (! $scriptVariables instanceof ArrayValue) {
            throw new InvalidArgumentException('WebhookScript variables could not be initialized.');
        }
        $this->scriptVariables = $scriptVariables;
    }

    /** @return array<string, callable> */
    public function functions(): array
    {
        return [
            'var' => fn (StringValue $name, ?Value $default = null): Value => $this->variable($name, $default),
            'set' => fn (StringValue $name, Value $value): Value => $this->set($name, $value),
            'respond' => fn (?Value $content = null, ?NumberValue $status = null, ?ArrayValue $headers = null): Value => $this->respond($content, $status, $headers),
            'set_response' => fn (?Value $content = null, ?NumberValue $status = null, ?ArrayValue $headers = null): Value => $this->setResponse($content, $status, $headers),
            'set_content' => fn (Value $content): Value => $this->setContent($content),
            'set_status' => fn (NumberValue $status): Value => $this->setStatus($status),
            'set_header' => fn (StringValue $name, Value $value): Value => $this->setHeader($name, $value),
            'echo' => fn (Value ...$values): Value => $this->log($values),
            'dump' => fn (Value ...$values): Value => $this->log($values),
            'print' => fn (Value ...$values): Value => $this->log($values),
            'stop' => fn (): Value => $this->stop(),
            'json_encode' => fn (Value $value): Value => new StringValue(json_encode($this->toPhp($value), JSON_THROW_ON_ERROR)),
            'json_decode' => fn (StringValue $value): Value => Value::buildAutomatic(json_decode($value->getInternalValue(), true, 128, JSON_THROW_ON_ERROR)),
            'base64_encode' => fn (Value $value): Value => new StringValue(base64_encode($this->body($value))),
            'base64_decode' => fn (StringValue $value): Value => new StringValue($this->decodeBase64($value)),
            'hash' => fn (Value $value, StringValue $algorithm): Value => $this->hash($value, $algorithm),
            'hmac' => fn (Value $value, StringValue $algorithm, StringValue $key): Value => $this->hmac($value, $algorithm, $key),
            'forward' => fn (StringValue $url, ?Value $content = null, ?StringValue $method = null, ?ArrayValue $headers = null): Value => $this->forward($url, $content, $method, $headers),
        ];
    }

    public function scriptVariables(): ArrayValue
    {
        return $this->scriptVariables;
    }

    public function outcome(): WebhookScriptOutcome
    {
        $variables = $this->toPhp($this->scriptVariables);

        return new WebhookScriptOutcome(
            response: $this->response,
            deliveries: $this->deliveries,
            variables: is_array($variables) ? $variables : [],
            logs: $this->logs,
        );
    }

    private function variable(StringValue $name, ?Value $default): Value
    {
        $path = trim($name->getInternalValue(), '$');
        $variables = $this->scriptVariables->getInternalValue();
        $value = array_key_exists($path, $variables)
            ? $variables[$path]
            : new WebhookScriptMissingValue;

        if ($value instanceof WebhookScriptMissingValue) {
            return $default ?? new NullValue;
        }

        return $value;
    }

    private function set(StringValue $name, Value $value): Value
    {
        $path = trim($name->getInternalValue(), '$');
        $this->scriptVariables->arraySet($path, Value::buildAutomatic($this->toPhp($value)));

        return new NullValue;
    }

    private function respond(?Value $content, ?NumberValue $status, ?ArrayValue $headers): Value
    {
        $this->response = $this->makeResponse($content, $status, $headers);

        throw new WebhookScriptResponseReady;
    }

    private function setResponse(?Value $content, ?NumberValue $status, ?ArrayValue $headers): Value
    {
        $this->response = $this->makeResponse($content, $status, $headers);

        return new NullValue;
    }

    private function setContent(Value $content): Value
    {
        $this->response = new Response(
            statusCode: $this->response?->statusCode ?? 200,
            headers: $this->response?->headers ?? [],
            body: $this->body($content),
        );

        return new NullValue;
    }

    private function setStatus(NumberValue $status): Value
    {
        $this->response = new Response(
            statusCode: $this->status($status),
            headers: $this->response?->headers ?? [],
            body: $this->response?->body ?? '',
        );

        return new NullValue;
    }

    private function setHeader(StringValue $name, Value $value): Value
    {
        $headers = $this->response?->headers ?? [];
        $headers[$name->getInternalValue()] = [(string) $this->scalar($value)];
        $this->response = new Response(
            statusCode: $this->response?->statusCode ?? 200,
            headers: $headers,
            body: $this->response?->body ?? '',
        );

        return new NullValue;
    }

    /** @param list<Value> $values */
    private function log(array $values): Value
    {
        if (count($this->logs) >= $this->maxLogs) {
            throw new InvalidArgumentException('WebhookScript exceeded the debug log limit.');
        }

        $message = implode(' ', array_map(
            fn (Value $value): string => is_scalar($php = $this->toPhp($value)) || $php === null
                ? (string) $php
                : json_encode($php, JSON_THROW_ON_ERROR),
            $values,
        ));
        $this->logs[] = mb_substr($message, 0, 4096);

        return new NullValue;
    }

    private function stop(): Value
    {
        throw new WebhookScriptResponseReady;
    }

    private function hash(Value $value, StringValue $algorithm): Value
    {
        $algorithm = strtolower($algorithm->getInternalValue());
        if (! in_array($algorithm, ['md5', 'sha1', 'sha256', 'sha384', 'sha512'], true)) {
            throw new InvalidArgumentException('WebhookScript hash algorithm is not allowed.');
        }

        return new StringValue(hash($algorithm, $this->body($value)));
    }

    private function hmac(Value $value, StringValue $algorithm, StringValue $key): Value
    {
        $algorithm = strtolower($algorithm->getInternalValue());
        if (! in_array($algorithm, ['md5', 'sha1', 'sha256', 'sha384', 'sha512'], true)) {
            throw new InvalidArgumentException('WebhookScript HMAC algorithm is not allowed.');
        }

        return new StringValue(hash_hmac($algorithm, $this->body($value), $key->getInternalValue()));
    }

    private function forward(StringValue $url, ?Value $content, ?StringValue $method, ?ArrayValue $headers): Value
    {
        if (count($this->deliveries) >= $this->maxDeliveries) {
            throw new InvalidArgumentException('WebhookScript exceeded the delivery limit.');
        }

        $this->deliveries[] = new Delivery(
            url: $url->getInternalValue(),
            method: strtoupper($method?->getInternalValue() ?? 'POST'),
            headers: $this->headers($headers),
            body: $content === null ? '' : $this->body($content),
        );

        return new NullValue;
    }

    private function makeResponse(?Value $content, ?NumberValue $status, ?ArrayValue $headers): Response
    {
        return new Response(
            statusCode: $status === null ? 200 : $this->status($status),
            headers: $this->headers($headers),
            body: $content === null ? '' : $this->body($content),
        );
    }

    private function status(NumberValue $status): int
    {
        $status = (int) $status->getInternalValue();
        if ($status < 100 || $status > 599) {
            throw new InvalidArgumentException('WebhookScript response status is outside the HTTP range.');
        }

        return $status;
    }

    /** @return array<string, list<string>> */
    private function headers(?ArrayValue $headers): array
    {
        if ($headers === null) {
            return [];
        }

        $normalized = [];
        foreach ($headers->getInternalValue() as $key => $value) {
            if (is_int($key)) {
                $line = (string) $this->scalar($value);
                [$name, $headerValue] = array_pad(explode(':', $line, 2), 2, null);
                if ($headerValue === null || trim($name) === '') {
                    throw new InvalidArgumentException('WebhookScript response header is invalid.');
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

    private function body(Value $value): string
    {
        $value = $this->toPhp($value);

        return is_array($value)
            ? json_encode($value, JSON_THROW_ON_ERROR)
            : (string) $value;
    }

    private function scalar(Value $value): string|int|float|bool|null
    {
        $value = $this->toPhp($value);
        if (! is_scalar($value) && $value !== null) {
            throw new InvalidArgumentException('WebhookScript value must be scalar.');
        }

        return $value;
    }

    private function toPhp(Value $value): mixed
    {
        return match (true) {
            $value instanceof NullValue => null,
            $value instanceof StringValue,
            $value instanceof NumberValue,
            $value instanceof BoolValue => $value->getInternalValue(),
            $value instanceof RegexValue => $value->getStringRepr(),
            $value instanceof ArrayValue => array_map(
                fn (Value $item): mixed => $this->toPhp($item),
                $value->getInternalValue(),
            ),
            default => $value->getStringValue(),
        };
    }

    private function decodeBase64(StringValue $value): string
    {
        $decoded = base64_decode($value->getInternalValue(), true);
        if ($decoded === false) {
            throw new InvalidArgumentException('WebhookScript base64 value is invalid.');
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
final class WebhookScriptMissingValue
{
}
