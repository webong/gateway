<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion;

use Closure;
use DateTimeImmutable;
use InvalidArgumentException;
use RuntimeException;
use Symfony\Component\Process\Exception\ProcessTimedOutException;
use Symfony\Component\Process\Process;
use Throwable;
use Webong\Gateway\Contracts\CompanionActionProvider;
use Webong\Gateway\Protocols\CompanionActionCapability;
use Webong\Gateway\Protocols\CompanionActionInvocation;
use Webong\Gateway\Protocols\CompanionActionResult;

/** Process-backed companion using ext.plugin/v1 JSON lines over stdin/stdout. */
final class ExtProcessCompanionActionProvider implements CompanionActionProvider
{
    private const string API_VERSION = 'ext.plugin/v1';

    private readonly WarmedCompanionProcessScope $warmedScope;

    /** @param array<string, mixed> $descriptor */
    public function __construct(
        private readonly string $extensionId,
        private readonly string $revision,
        private readonly CompanionActionCapability $providedCapability,
        private readonly array $descriptor,
        private readonly string $executable,
        private readonly string $workingDirectory,
        private readonly int $maxRuntimeSeconds,
        private readonly int $maxOutputBytes = 1048576,
        private readonly string $phpBinary = PHP_BINARY,
        private readonly ?Closure $verifyGeneration = null,
        private readonly ?Closure $executionPolicy = null,
        ?WarmedCompanionProcessScope $warmedScope = null,
    ) {
        if ($maxRuntimeSeconds < 1 || $maxOutputBytes < 1 || trim($phpBinary) === '' || ! is_file($executable) || ! is_dir($workingDirectory)) {
            throw new InvalidArgumentException('Invalid ext companion process configuration.');
        }
        $this->warmedScope = $warmedScope ?? new WarmedCompanionProcessScope;
    }

    public function extension(): string
    {
        return $this->extensionId;
    }

    public function mode(): string
    {
        return 'dynamic';
    }

    public function providerId(): string
    {
        return 'dynamic:'.$this->extensionId.':'.$this->revision.':'.$this->providedCapability->name;
    }

    public function capability(): CompanionActionCapability
    {
        return $this->providedCapability;
    }

    public function close(): void
    {
        $this->stopWarmedProcess();
    }

    public function invoke(CompanionActionInvocation $invocation): CompanionActionResult
    {
        $timeout = $this->invocationTimeout($invocation);
        $policy = $this->policy();
        if ($policy->mode === CompanionExecutionPolicy::ISOLATED) {
            $this->stopWarmedProcess();
            $this->verifyGeneration();

            return $this->invokeIsolated($invocation, $timeout);
        }

        return $this->invokeWarmed($invocation, $timeout, $policy);
    }

    private function invokeIsolated(CompanionActionInvocation $invocation, float $timeout): CompanionActionResult
    {
        $requestId = $this->requestId($invocation);
        $input = $this->helloFrame($timeout)."\n".$this->invocationFrame($invocation, $requestId)."\n";
        $process = new Process(
            $this->command(),
            $this->workingDirectory,
            $this->processEnvironment(),
            $input,
            $timeout,
        );
        $output = '';
        $outputBytes = 0;
        $outputExceeded = false;
        $process->disableOutput();
        try {
            $exitCode = $process->run(function (string $type, string $buffer) use (&$output, &$outputBytes, &$outputExceeded): void {
                $outputBytes += strlen($buffer);
                if ($outputBytes > $this->maxOutputBytes) {
                    $outputExceeded = true;

                    return;
                }
                if ($type === Process::OUT) {
                    $output .= $buffer;
                }
            });
        } catch (ProcessTimedOutException $exception) {
            throw new RuntimeException('Gateway companion process timed out.', previous: $exception);
        } catch (Throwable $exception) {
            throw new RuntimeException('Gateway companion process failed.', previous: $exception);
        }
        if ($outputExceeded) {
            throw new RuntimeException('Gateway companion process exceeded its output limit.');
        }
        if ($exitCode !== 0) {
            throw new RuntimeException('Gateway companion process failed.');
        }

        $lines = preg_split('/\r?\n/', trim($output));
        if (! is_array($lines) || count($lines) !== 2) {
            throw new RuntimeException('Gateway companion process returned an invalid frame count.');
        }
        $this->validateHello($lines[0]);

        return $this->result($lines[1], $requestId);
    }

    private function invokeWarmed(
        CompanionActionInvocation $invocation,
        float $timeout,
        CompanionExecutionPolicy $policy,
    ): CompanionActionResult {
        $now = microtime(true);
        if ($this->warmedScope->process !== null && (
            $this->warmedScope->policyKey !== $policy->key()
            || ! $this->warmedScope->process->isRunning()
            || $this->warmedScope->process->invocations >= $policy->maxInvocations
            || $now - $this->warmedScope->process->startedAt >= $policy->maxAgeSeconds
            || $now - $this->warmedScope->process->lastUsedAt >= $policy->idleTimeoutSeconds
        )) {
            $this->stopWarmedProcess();
        }

        try {
            if ($this->warmedScope->process === null) {
                $this->verifyGeneration();
                $this->warmedScope->process = new WarmedCompanionProcess(
                    $this->command(),
                    $this->workingDirectory,
                    $this->processEnvironment(),
                );
                $this->warmedScope->policyKey = $policy->key();
                $this->validateHello($this->warmedScope->process->exchange(
                    $this->helloFrame($timeout),
                    $timeout,
                    $this->maxOutputBytes,
                ));
            }

            $requestId = $this->requestId($invocation);
            $response = $this->warmedScope->process->exchange(
                $this->invocationFrame($invocation, $requestId),
                $this->invocationTimeout($invocation),
                $this->maxOutputBytes,
            );
            $this->warmedScope->process->invocations++;

            return $this->result($response, $requestId);
        } catch (Throwable $exception) {
            $this->stopWarmedProcess();

            throw $exception;
        }
    }

    private function policy(): CompanionExecutionPolicy
    {
        $policy = $this->executionPolicy === null
            ? new CompanionExecutionPolicy
            : ($this->executionPolicy)();
        if (! $policy instanceof CompanionExecutionPolicy) {
            throw new RuntimeException('Gateway companion execution policy is invalid.');
        }

        return $policy;
    }

    private function stopWarmedProcess(): void
    {
        $this->warmedScope->stop();
    }

    private function verifyGeneration(): void
    {
        if ($this->verifyGeneration === null) {
            return;
        }

        try {
            ($this->verifyGeneration)();
        } catch (Throwable $exception) {
            $this->stopWarmedProcess();

            throw $exception;
        }
    }

    /** @return list<string> */
    private function command(): array
    {
        return [
            $this->phpBinary,
            '-d', 'display_errors=stderr',
            '-d', 'html_errors=0',
            '-d', 'opcache.enable_cli=0',
            $this->executable,
        ];
    }

    private function helloFrame(float $timeout): string
    {
        return $this->encode([
            'apiVersion' => self::API_VERSION,
            'id' => 'hello',
            'plugin' => ['id' => '', 'revision' => ''],
            'contract' => ['name' => '', 'version' => ''],
            'operation' => 'plugin.hello',
            'deadline' => $this->deadline($timeout),
        ]);
    }

    private function invocationFrame(CompanionActionInvocation $invocation, string $requestId): string
    {
        return $this->encode([
            'apiVersion' => self::API_VERSION,
            'id' => $requestId,
            'plugin' => $this->descriptor['identity'],
            'contract' => ['name' => 'gateway.companion', 'version' => CompanionActionInvocation::VERSION],
            'operation' => $invocation->action,
            'deadline' => $invocation->deadline->format(DATE_RFC3339_EXTENDED),
            'payload' => $invocation->toArray(),
        ]);
    }

    private function requestId(CompanionActionInvocation $invocation): string
    {
        return 'invoke-'.substr(hash('sha256', $invocation->executionId), 0, 24);
    }

    private function invocationTimeout(CompanionActionInvocation $invocation): float
    {
        $remaining = (float) $invocation->deadline->format('U.u') - microtime(true);
        if ($remaining <= 0) {
            throw new InvalidArgumentException('Gateway companion action deadline has expired.');
        }

        return min((float) $this->maxRuntimeSeconds, $remaining);
    }

    private function validateHello(string $line): void
    {
        $hello = $this->decodeResponse($line, 'hello');
        if (($hello['payload'] ?? null) != $this->descriptor) {
            throw new RuntimeException('Gateway companion process handshake did not match its reviewed descriptor.');
        }
    }

    private function result(string $line, string $requestId): CompanionActionResult
    {
        $response = $this->decodeResponse($line, $requestId);
        if (! is_array($response['payload'] ?? null)) {
            throw new RuntimeException('Gateway companion process returned an invalid result payload.');
        }

        return CompanionActionResult::fromArray($response['payload']);
    }

    private function deadline(float $timeout): string
    {
        return DateTimeImmutable::createFromFormat('U.u', sprintf('%.6F', microtime(true) + $timeout))
            ->format(DATE_RFC3339_EXTENDED);
    }

    /**
     * Symfony Process otherwise inherits the whole Planner environment. False
     * removes inherited names, leaving only the extension identity and PATH.
     *
     * @return array<string, string|false>
     */
    private function processEnvironment(): array
    {
        $inherited = array_merge(
            is_array(getenv()) ? getenv() : [],
            $_ENV,
            $_SERVER,
        );
        $environment = array_fill_keys(array_filter(
            array_keys($inherited),
            static fn (mixed $name): bool => is_string($name) && $name !== '',
        ), false);
        $environment['PATH'] = (string) getenv('PATH');
        $environment['GATEWAY_EXT_ID'] = $this->extensionId;
        $environment['GATEWAY_EXT_REVISION'] = $this->revision;
        $environment['GATEWAY_EXT_VERSION'] = $this->providedCapability->version ?? '';

        return $environment;
    }

    /** @param array<string, mixed> $value */
    private function encode(array $value): string
    {
        return json_encode($value, JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES);
    }

    /** @return array<string, mixed> */
    private function decodeResponse(string $line, string $id): array
    {
        $response = json_decode($line, true, 128, JSON_THROW_ON_ERROR);
        if (! is_array($response) || ($response['apiVersion'] ?? null) !== self::API_VERSION
            || ($response['id'] ?? null) !== $id
            || (array_key_exists('payload', $response) === array_key_exists('error', $response))) {
            throw new RuntimeException('Gateway companion process returned an invalid ext response.');
        }
        if (isset($response['error'])) {
            throw new RuntimeException('Gateway companion process refused the invocation.');
        }

        return $response;
    }

}
