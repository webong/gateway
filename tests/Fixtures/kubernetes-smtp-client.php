<?php

declare(strict_types=1);

if ($argc !== 4 && ($argc !== 5 || $argv[4] !== '--send')) {
    fwrite(STDERR, "Usage: php kubernetes-smtp-client.php NODE_IP NODE_PORT CA_CERT [--send]\n");
    exit(2);
}

$context = stream_context_create([
    'ssl' => [
        'verify_peer' => true,
        'verify_peer_name' => true,
        'peer_name' => 'smtp.managed.test',
        'cafile' => $argv[3],
    ]
]);
$socket = stream_socket_client("tcp://{$argv[1]}:{$argv[2]}", $errno, $error, 5, STREAM_CLIENT_CONNECT, $context);
if (!is_resource($socket)) {
    throw new RuntimeException("SMTP connection failed: {$errno} {$error}");
}
stream_set_timeout($socket, 5);

function smtpReply($socket, int $expected): string
{
    $reply = '';
    do {
        $line = fgets($socket);
        if ($line === false) {
            throw new RuntimeException('SMTP connection ended before a complete reply.');
        }
        $reply .= $line;
    } while (isset($line[3]) && $line[3] === '-');

    if ((int) substr($line, 0, 3) !== $expected) {
        throw new RuntimeException("Expected SMTP {$expected}, got {$reply}");
    }

    return $reply;
}

function smtpCommand($socket, string $command, int $expected): string
{
    if (fwrite($socket, $command . "\r\n") === false) {
        throw new RuntimeException("Could not write SMTP command {$command}.");
    }

    return smtpReply($socket, $expected);
}

try {
    smtpReply($socket, 220);
    $ehlo = smtpCommand($socket, 'EHLO smtp-client.example.test', 250);
    if (!str_contains($ehlo, 'STARTTLS')) {
        throw new RuntimeException('SMTP did not advertise STARTTLS.');
    }
    smtpCommand($socket, 'STARTTLS', 220);
    if (stream_socket_enable_crypto($socket, true, STREAM_CRYPTO_METHOD_TLS_CLIENT) !== true) {
        throw new RuntimeException('SMTP STARTTLS failed.');
    }
    smtpCommand($socket, 'EHLO smtp-client.example.test', 250);
    smtpCommand($socket, 'MAIL FROM:<sender@example.net>', 250);
    smtpCommand($socket, 'RCPT TO:<first@managed.test>', 250);
    smtpCommand($socket, 'RCPT TO:<unknown@managed.test>', 550);
    if ($argc === 5) {
        smtpCommand($socket, 'DATA', 354);
        $message = "From: sender@example.net\r\nTo: first@managed.test\r\nSubject: kubernetes relay smoke\r\n\r\nGateway relay payload\r\n.\r\n";
        if (fwrite($socket, $message) !== strlen($message)) {
            throw new RuntimeException('Could not submit the SMTP message.');
        }
        smtpReply($socket, 250);
    }
    smtpCommand($socket, 'QUIT', 221);
    echo $argc === 5
        ? "STARTTLS, recipient decisions, and SMTP DATA submission passed.\n"
        : "STARTTLS and managed/unknown recipient decisions passed.\n";
} finally {
    fclose($socket);
}
