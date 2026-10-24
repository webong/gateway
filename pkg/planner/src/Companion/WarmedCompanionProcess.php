<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion;

use RuntimeException;
use Symfony\Component\Process\InputStream;
use Symfony\Component\Process\Process;

/** One bounded ext.plugin/v1 process that exchanges one JSON-line frame at a time. */
final class WarmedCompanionProcess
{
    private readonly InputStream $input;
    private readonly Process $process;
    private string $output = '';

    public int $invocations = 0;
    public readonly float $startedAt;
    public float $lastUsedAt;

    /**
     * @param list<string> $command
     * @param array<string, string|false> $environment
     */
    public function __construct(array $command, string $workingDirectory, array $environment)
    {
        $this->input = new InputStream;
        $this->process = new Process($command, $workingDirectory, $environment, $this->input, null);
        $this->process->setIdleTimeout(null);
        $this->process->start();
        $this->startedAt = $this->lastUsedAt = microtime(true);
    }

    public function exchange(string $frame, float $timeout, int $maxOutputBytes): string
    {
        if ($timeout <= 0 || $maxOutputBytes < 1 || ! $this->isRunning()) {
            throw new RuntimeException('Gateway warmed companion process is unavailable.');
        }

        $this->input->write($frame."\n");
        $deadline = microtime(true) + $timeout;
        $observedBytes = 0;

        while (true) {
            $running = $this->process->isRunning();
            $stdout = $this->process->getIncrementalOutput();
            $stderr = $this->process->getIncrementalErrorOutput();
            $observedBytes += strlen($stdout) + strlen($stderr);
            if ($observedBytes > $maxOutputBytes) {
                $this->stop();

                throw new RuntimeException('Gateway companion process exceeded its output limit.');
            }
            $this->output .= $stdout;
            $newline = strpos($this->output, "\n");
            if ($newline !== false) {
                $line = rtrim(substr($this->output, 0, $newline), "\r");
                $this->output = substr($this->output, $newline + 1);
                if (trim($this->output) !== '') {
                    $this->stop();

                    throw new RuntimeException('Gateway warmed companion process returned unexpected output.');
                }
                $this->lastUsedAt = microtime(true);

                return $line;
            }
            if (! $running) {
                $this->stop();

                throw new RuntimeException('Gateway companion process failed.');
            }
            if (microtime(true) >= $deadline) {
                $this->stop();

                throw new RuntimeException('Gateway companion process timed out.');
            }

            usleep(1000);
        }
    }

    public function isRunning(): bool
    {
        return $this->process->isRunning();
    }

    public function stop(): void
    {
        if (! $this->input->isClosed()) {
            $this->input->close();
        }
        if ($this->process->isRunning()) {
            $this->process->stop(0.1);
        }
        $this->output = '';
    }

    public function __destruct()
    {
        $this->stop();
    }
}
