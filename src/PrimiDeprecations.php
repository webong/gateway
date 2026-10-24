<?php

declare(strict_types=1);

namespace Webong\Gateway\HookScript;

/** @internal Remove PHP-version noise from the legacy Primi dependency only. */
final class PrimiDeprecations
{
    public static function silence(callable $callback): mixed
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
