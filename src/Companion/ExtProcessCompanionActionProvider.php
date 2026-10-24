<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion;

use Closure;
use DateTimeImmutable;
use InvalidArgumentException;
use RuntimeException;
use Symfony\Component\Process\Exception\ProcessTimedOutException;
use Symfony\Component\Process\Process;
use Webong\Gateway\Contracts\CompanionActionProvider;
use Webong\Gateway\Protocols\CompanionActionCapability;
use Webong\Gateway\Protocols\CompanionActionInvocation;
use Webong\Gateway\Protocols\CompanionActionResult;

/** Process-backed companion using ext.plugin/v1 JSON lines over stdin/stdout. */
final readonly class ExtProcessCompanionActionProvider implements CompanionActionProvider
{
    private const string API_VERSION = 'ext.plugin/v1';

    /** @param array<string, mixed> $descriptor */
    public function __construct(
        private string $extensionId,
        private string $revision,
        private CompanionActionCapability $providedCapability,
        private array $descriptor,
        private string $executable,
        private string $workingDirectory,
        private int $maxRuntimeSeconds,
        private string $phpBinary = PHP_BINARY,
        private ?Closure $verifyPackage = null,
    ) {
        if ($maxRuntimeSeconds < 1 || trim($phpBinary) === '' || ! is_file($executable) || ! is_dir($workingDirectory)) {
            throw new InvalidArgumentException('Invalid ext companion process configuration.');
        }
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

    public function invoke(CompanionActionInvocation $invocation): CompanionActionResult
    {
        if ($this->verifyPackage !== null) {
            ($this->verifyPackage)();
        }
        $remaining = (float) $invocation->deadline->format('U.u') - microtime(true);
        if ($remaining <= 0) {
            throw new InvalidArgumentException('Gateway companion action deadline has expired.');
        }
        $timeout = min((float) $this->maxRuntimeSeconds, $remaining);
        $requestId = 'invoke-'.substr(hash('sha256', $invocation->executionId), 0, 24);
        $input = $this->encode([
            'apiVersion' => self::API_VERSION,
            'id' => 'hello',
            'plugin' => ['id' => '', 'revision' => ''],
            'contract' => ['name' => '', 'version' => ''],
            'operation' => 'plugin.hello',
            'deadline' => $this->deadline($timeout),
        ])."\n".$this->encode([
            'apiVersion' => self::API_VERSION,
            'id' => $requestId,
            'plugin' => $this->descriptor['identity'],
            'contract' => ['name' => 'gateway.companion', 'version' => CompanionActionInvocation::VERSION],
            'operation' => $invocation->action,
            'deadline' => $invocation->deadline->format(DATE_RFC3339_EXTENDED),
            'payload' => $invocation->toArray(),
        ])."\n";

        $process = new Process(
            [$this->phpBinary, '-d', 'display_errors=stderr', '-d', 'html_errors=0', $this->executable],
            $this->workingDirectory,
            $this->processEnvironment(),
            $input,
            $timeout,
        );
        try {
            $process->mustRun();
        } catch (ProcessTimedOutException $exception) {
            throw new RuntimeException('Gateway companion process timed out.', previous: $exception);
        } catch (\Throwable $exception) {
            throw new RuntimeException('Gateway companion process failed.', previous: $exception);
        }

        $lines = preg_split('/\r?\n/', trim($process->getOutput()));
        if (! is_array($lines) || count($lines) !== 2) {
            throw new RuntimeException('Gateway companion process returned an invalid frame count.');
        }
        $hello = $this->decodeResponse($lines[0], 'hello');
        if (($hello['payload'] ?? null) != $this->descriptor) {
            throw new RuntimeException('Gateway companion process handshake did not match its reviewed descriptor.');
        }
        $response = $this->decodeResponse($lines[1], $requestId);
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
