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

        /** @var HookScriptOutcome */
        return PrimiDeprecations::silence(
            fn () => $this->executeWithPrimi($source, $request, $variables),
        );
    }

    /** @param array<string, mixed> $variables */
    private function executeWithPrimi(string $source, IngressRequest $request, array $variables): HookScriptOutcome
    {
        $host = new HookScriptHost(
            $request,
            $this->maxDeliveries,
            $this->maxSchedules,
            $this->maxLogs,
            $variables,
        );

        try {
            $context = new Context;
            $interpreter = new Interpreter($context);
            $context->setVariables($host->functions());
            $context->setVariable('variables', $host->scriptVariables());

            $previousAsyncSignals = pcntl_async_signals(true);
            $previousAlarmHandler = pcntl_signal_get_handler(SIGALRM);
            pcntl_signal(SIGALRM, static function (): never {
                throw new HookScriptTimedOut('HookScript execution timed out.');
            });
            pcntl_alarm(max(1, $this->timeoutSeconds));

            try {
                $interpreter->run($source);
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
        } catch (Throwable $exception) {
            if ($exception instanceof PrimiError) {
                throw new HookScriptExecutionException('HookScript is invalid: '.$exception->getMessage(), previous: $exception);
            }

            throw new HookScriptExecutionException('HookScript execution failed.', previous: $exception);
        }

        return $host->outcome();
    }
}
