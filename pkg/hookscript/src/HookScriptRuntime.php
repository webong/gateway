<?php

declare(strict_types=1);

namespace Webong\Gateway\HookScript;

use Smuuf\Primi\Context;
use Smuuf\Primi\ErrorException as PrimiError;
use Smuuf\Primi\Interpreter;
use Throwable;
use Webong\Gateway\Protocols\IngressRequest;

final readonly class HookScriptRuntime
{
    public function __construct(
        private int $maxSourceBytes = 65536,
        private int $timeoutSeconds = 1,
        private int $maxDeliveries = 32,
        private int $maxLogs = 100,
        private int $maxSchedules = 8,
    ) {
    }

    /** @param array<string, mixed> $variables */
    public function execute(string $source, IngressRequest $request, array $variables = []): HookScriptOutcome
    {
        if (trim($source) === '') {
            throw new HookScriptExecutionException('HookScript source is required.');
        }
        if (strlen($source) > $this->maxSourceBytes) {
            throw new HookScriptExecutionException('HookScript source exceeds the configured limit.');
        }
        if (! function_exists('pcntl_alarm') || ! function_exists('pcntl_signal')) {
            throw new HookScriptExecutionException('HookScript execution requires the PCNTL extension.');
        }

        $host = new HookScriptHost($request, $this->maxDeliveries, $this->maxSchedules, $this->maxLogs, $variables);

        try {
            $interpreter = $this->withoutVendorDeprecations(function () use ($host): Interpreter {
                $context = new Context;
                $interpreter = new Interpreter($context);
                $context->setVariables($host->functions());
                $context->setVariable('variables', $host->scriptVariables());

                return $interpreter;
            });

            $previousAsyncSignals = pcntl_async_signals(true);
            $previousAlarmHandler = pcntl_signal_get_handler(SIGALRM);
            pcntl_signal(SIGALRM, static function (): never {
                throw new HookScriptTimedOut('HookScript execution timed out.');
            });
            pcntl_alarm(max(1, $this->timeoutSeconds));

            try {
                $this->withoutVendorDeprecations(fn () => $interpreter->run($source));
            } catch (HookScriptResponseReady) {
                // respond() and stop() terminate normal script execution.
            } finally {
                pcntl_alarm(0);
                pcntl_signal(SIGALRM, $previousAlarmHandler);
                pcntl_async_signals($previousAsyncSignals);
            }
        } catch (HookScriptTimedOut $exception) {
            throw new HookScriptExecutionException($exception->getMessage(), previous: $exception);
        } catch (HookScriptResponseReady) {
            // A response thrown while constructing the interpreter is impossible,
            // but keep the control-flow exception from being wrapped if Primi changes.
        } catch (PrimiError $exception) {
            throw new HookScriptExecutionException('HookScript is invalid: '.$exception->getMessage(), previous: $exception);
        } catch (Throwable $exception) {
            throw new HookScriptExecutionException('HookScript execution failed.', previous: $exception);
        }

        return $host->outcome();
    }

    private function withoutVendorDeprecations(callable $callback): mixed
    {
        $previousHandler = null;
        $previousHandler = set_error_handler(
            static function (int $severity, string $message, string $file, int $line) use (&$previousHandler): bool {
                if (in_array($severity, [E_DEPRECATED, E_USER_DEPRECATED], true)
                    && str_contains($file, DIRECTORY_SEPARATOR.'vendor'.DIRECTORY_SEPARATOR.'smuuf'.DIRECTORY_SEPARATOR)) {
                    return true;
                }

                return is_callable($previousHandler)
                    ? (bool) $previousHandler($severity, $message, $file, $line)
                    : false;
            },
        );

        try {
            return $callback();
        } finally {
            restore_error_handler();
        }
    }
}
