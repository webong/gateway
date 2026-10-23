<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use InvalidArgumentException;

/** Replace sender headers before a shared-domain message reaches the queue. */
final class SharedSenderMessage
{
    public static function rewrite(string $message, string $name, string $from, string $replyTo): string
    {
        if ($name === '' || strlen($name) > 120 || preg_match('/[\x00-\x1f\x7f]/', $name) === 1
            || ! mb_check_encoding($name, 'UTF-8')
            || filter_var($from, FILTER_VALIDATE_EMAIL) === false
            || filter_var($replyTo, FILTER_VALIDATE_EMAIL) === false) {
            throw new InvalidArgumentException('Invalid shared sender identity.');
        }
        if (preg_match('/\r?\n\r?\n/', $message, $match, PREG_OFFSET_CAPTURE) !== 1
            || $match[0][1] > 65536) {
            throw new InvalidArgumentException('Invalid message headers.');
        }
        $separator = $match[0][0];
        $split = $match[0][1];
        $header = str_replace("\r\n", "\n", substr($message, 0, $split));
        if (str_contains($header, "\r")) { throw new InvalidArgumentException('Invalid message header.'); }
        $body = substr($message, $split + strlen($separator));
        $lines = explode("\n", $header);
        $kept = [];
        $skip = false;
        $fromCount = 0;
        foreach ($lines as $line) {
            if (str_starts_with($line, ' ') || str_starts_with($line, "\t")) {
                if ($kept === [] && ! $skip) { throw new InvalidArgumentException('Invalid folded header.'); }
                if (! $skip) { $kept[] = $line; }
                continue;
            }
            if (preg_match('/^([A-Za-z0-9-]+):/', $line, $match) !== 1) {
                throw new InvalidArgumentException('Invalid message header.');
            }
            $field = strtolower($match[1]);
            if (in_array($field, ['sender', 'resent-from', 'resent-sender', 'return-path'], true)) {
                throw new InvalidArgumentException('Conflicting sender header.');
            }
            if ($field === 'from') { $fromCount++; }
            $skip = $field === 'from' || $field === 'reply-to';
            if (! $skip) { $kept[] = $line; }
        }
        if ($fromCount !== 1) { throw new InvalidArgumentException('One From header is required.'); }
        $displayName = preg_match('/^[\x20-\x7e]+$/D', $name) === 1
            ? '"'.addcslashes($name, '"\\').'"'
            : mb_encode_mimeheader($name, 'UTF-8', 'B', "\r\n");
        return 'From: '.$displayName.' <'.$from.">\r\nReply-To: <".$replyTo.">\r\n"
            .implode("\r\n", $kept)."\r\n\r\n".$body;
    }
}
