<?php

declare(strict_types=1);

$address = $argv[1] ?? 'router';
$port = isset($argv[2]) ? (int) $argv[2] : 8080;
$trustedCertificate = $argv[3] ?? null;
if ($port < 1 || $port > 65535) {
    throw new InvalidArgumentException('Invalid Router port.');
}
function request(string $address, int $port, string $path, array $headers = [], ?string $trustedCertificate = null): array
{
    $context = stream_context_create($trustedCertificate === null ? [] : ['ssl' => [
        'verify_peer' => true,
        'verify_peer_name' => true,
        'peer_name' => 'live.example.test',
        'cafile' => $trustedCertificate,
        'SNI_enabled' => true,
    ]]);
    $transport = $trustedCertificate === null ? 'tcp' : 'tls';
    $socket = @stream_socket_client("$transport://$address:$port", $error, $message, 5, STREAM_CLIENT_CONNECT, $context);
    if ($socket === false) {
        throw new RuntimeException("Router connection failed: $message ($error)");
    }
    stream_set_timeout($socket, 5);
    $request = "GET $path HTTP/1.1\r\nHost: live.example.test\r\n";
    if (! array_key_exists('Connection', $headers)) {
        $request .= "Connection: close\r\n";
    }
    foreach ($headers as $name => $value) {
        $request .= "$name: $value\r\n";
    }
    fwrite($socket, $request."\r\n");
    $status = fgets($socket);
    $responseHeaders = [];
    while (($line = fgets($socket)) !== false && trim($line) !== '') {
        [$name, $value] = array_pad(explode(':', $line, 2), 2, '');
        $responseHeaders[strtolower(trim($name))] = trim($value);
    }
    fclose($socket);
    return [$status, $responseHeaders];
}

if (($argv[1] ?? '') === 'denied') {
    [$status] = request('router', 8080, '/connection/websocket');
    if (! is_string($status) || ! str_contains($status, ' 502 ')) {
        throw new RuntimeException('Out-of-range Pod IP was not rejected: '.var_export($status, true));
    }
    echo "Out-of-range Pod IP was rejected with 502.\n";
    exit(0);
}

$key = base64_encode(random_bytes(16));
[$status, $headers] = request($address, $port, '/connection/websocket', [
    'Upgrade' => 'websocket',
    'Connection' => 'Upgrade',
    'Sec-WebSocket-Version' => '13',
    'Sec-WebSocket-Key' => $key,
], $trustedCertificate);
$expected = base64_encode(sha1($key.'258EAFA5-E914-47DA-95CA-C5AB0DC85B11', true));
if (! is_string($status) || ! str_contains($status, ' 101 ') ||
    ($headers['sec-websocket-accept'] ?? null) !== $expected) {
    fwrite(STDERR, 'Managed WebSocket upgrade failed: '.var_export([$status, $headers], true)."\n");
    exit(1);
}

[$privateStatus] = request($address, $port, '/_internal/provisioning/routes/resolve?hostname=live.example.test&path=%2Fconnection', [], $trustedCertificate);
if (! str_contains($privateStatus, ' 404 ')) {
    fwrite(STDERR, "Private Planner route was exposed: $privateStatus\n");
    exit(1);
}

echo "Managed WebSocket upgrade reached the live Centrifugo Pod through Router; private Planner route stayed hidden.\n";
