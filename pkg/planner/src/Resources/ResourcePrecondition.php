<?php

declare(strict_types=1);

namespace Webong\Gateway\Resources;

use Illuminate\Http\Request;

final class ResourcePrecondition
{
    public static function revision(Request $request): ?int
    {
        $header = $request->header('If-Match');
        if (! is_string($header) || preg_match('/^"([1-9][0-9]*)"$/D', $header, $matches) !== 1) {
            return null;
        }
        $revision = filter_var($matches[1], FILTER_VALIDATE_INT);

        return $revision === false ? null : $revision;
    }

    public static function etag(int $revision): string
    {
        return '"'.$revision.'"';
    }
}
