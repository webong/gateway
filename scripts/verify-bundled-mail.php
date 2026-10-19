<?php

declare(strict_types=1);

// Run only against a disposable Router volume and a disposable TLS mailbox
// fixture. Never point this script at a customer's mailbox or mail server.

$password = getenv('GATEWAY_TEST_MAILBOX_PASSWORD');
$host = getenv('GATEWAY_TEST_MAILBOX_HOST') ?: 'gateway-mailbox';
$imapPort = (int) (getenv('GATEWAY_TEST_MAILBOX_IMAP_PORT') ?: 31993);
$smtpPort = (int) (getenv('GATEWAY_TEST_MAILBOX_SMTP_PORT') ?: 31465);
if (! is_string($password) || $password === '') {
    fwrite(STDERR, "GATEWAY_TEST_MAILBOX_PASSWORD is required.\n");
    exit(2);
}
$credentials = file('/data/gateway.env', FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES);
$tokens = [];
foreach ($credentials ?: [] as $line) {
    [$key, $value] = array_pad(explode('=', $line, 2), 2, '');
    $tokens[$key] = $value;
}
if (($tokens['REGISTRY_TOKEN'] ?? '') === '' || ($tokens['GATEWAY_INTERNAL_TOKEN'] ?? '') === '') {
    throw new RuntimeException('Router bootstrap credentials are unavailable.');
}

/** @return array{0:int,1:array<string,mixed>} */
function gatewayRequest(string $method, string $path, array $headers = [], ?array $body = null,
    string $origin = 'https://localhost:8443'): array
{
    $headers[] = 'Accept: application/json';
    if ($body !== null) {
        $headers[] = 'Content-Type: application/json';
    }
    $context = stream_context_create([
        'http' => [
            'method' => $method, 'header' => implode("\r\n", $headers),
            'content' => $body === null ? '' : json_encode($body, JSON_THROW_ON_ERROR),
            'ignore_errors' => true, 'timeout' => 45,
        ],
        'ssl' => [
            'verify_peer' => true, 'verify_peer_name' => true,
            'peer_name' => 'localhost', 'cafile' => '/data/tls/server.crt',
        ],
    ]);
    $response = @file_get_contents($origin.$path, false, $context);
    $statusLine = $http_response_header[0] ?? '';
    if (! is_string($response) || ! preg_match('/^HTTP\/\S+\s+(\d{3})/', $statusLine, $match)) {
        throw new RuntimeException("Gateway request failed: {$method} {$path}");
    }
    $decoded = json_decode($response, true);
    $GLOBALS['gatewayLastMessage'] = is_array($decoded) && is_string($decoded['message'] ?? null)
        ? $decoded['message'] : '';

    return [(int) $match[1], is_array($decoded) ? $decoded : []];
}

function expectStatus(int $actual, int $expected, string $step): void
{
    if ($actual !== $expected) {
        throw new RuntimeException("{$step}: HTTP {$actual}, expected {$expected}; ".($GLOBALS['gatewayLastMessage'] ?? ''));
    }
}

/** @return resource */
function loginToGatewayIMAP(string $id, string $password)
{
    $socket = @stream_socket_client('tls://127.0.0.1:993', $errno, $error, 10,
        STREAM_CLIENT_CONNECT, stream_context_create(['ssl' => [
            'verify_peer' => true, 'verify_peer_name' => true,
            'peer_name' => 'localhost', 'cafile' => '/data/tls/server.crt',
        ]]));
    if (! is_resource($socket)) { throw new RuntimeException('Bundled IMAP TLS listener is unavailable.'); }
    stream_set_timeout($socket, 10);
    if (! str_starts_with((string) fgets($socket), '* OK')) {
        throw new RuntimeException('Bundled IMAP greeting is invalid.');
    }
    fwrite($socket, 'a1 LOGIN "'.$id.'" "'.$password."\"\r\n");
    for ($i = 0; $i < 32; $i++) {
        $line = fgets($socket);
        if ($line === false) { break; }
        if (str_starts_with($line, 'a1 ')) {
            if (! str_starts_with($line, 'a1 OK')) {
                throw new RuntimeException('Bundled IMAP login was rejected.');
            }
            return $socket;
        }
    }
    throw new RuntimeException('Bundled IMAP login did not complete.');
}

$operator = ['Authorization: Bearer '.$tokens['REGISTRY_TOKEN']];
$suffix = bin2hex(random_bytes(4));
[$status] = gatewayRequest('GET', '/_internal/gateway/ready');
expectStatus($status, 404, 'public private-route isolation');
[$status, $policy] = gatewayRequest('GET', '/email/runtime-policy', $operator);
if ($status === 404) {
    [$status, $policy] = gatewayRequest('POST', '/email/runtime-policy', $operator,
        ['connected_smtp_enabled' => true, 'http_submission_enabled' => true]);
    expectStatus($status, 201, 'Planner runtime policy');
} else {
    expectStatus($status, 200, 'Planner runtime policy');
}
if (($policy['connected_smtp_enabled'] ?? null) !== true || ($policy['http_submission_enabled'] ?? null) !== true) {
    throw new RuntimeException('Planner mail submission policy is not enabled.');
}
foreach (['imap' => $imapPort, 'smtp' => $smtpPort] as $protocol => $port) {
    [$status] = gatewayRequest('POST', '/email/upstreams', $operator, [
        'protocol' => $protocol, 'name' => $protocol.'-'.$suffix,
        'address' => $host.':'.$port, 'server_name' => $host, 'network_scope' => 'private',
        ...($protocol === 'smtp' ? ['tls_mode' => 'implicit'] : []),
    ]);
    expectStatus($status, 201, "Planner {$protocol} upstream");
}

$connections = [];
foreach (['alpha', 'beta'] as $label) {
    $accountId = 'bundle-'.$label.'-'.$suffix;
    [$status, $account] = gatewayRequest('POST', '/accounts', $operator, ['id' => $accountId]);
    expectStatus($status, 201, "{$label} account");
    $accountToken = $account['token'] ?? null;
    if (! is_string($accountToken) || $accountToken === '') { throw new RuntimeException('Account token missing.'); }
    $accountHeaders = ['Authorization: Bearer '.$accountToken];
    $address = $label.'-'.$suffix.'@example.test';
    [$status, $created] = gatewayRequest('POST', "/email/mailbox-accounts/{$accountId}/connections", $accountHeaders, [
        'address' => $address, 'imap_upstream_id' => 'imap-'.$suffix,
        'smtp_upstream_id' => 'smtp-'.$suffix, 'imap_username' => $address,
        'smtp_username' => $address, 'imap_secret' => $password,
        'smtp_secret' => $password, 'imap_auth' => 'password', 'smtp_auth' => 'password',
    ]);
    expectStatus($status, 201, "{$label} connection");
    $id = $created['connection']['id'] ?? null;
    $imapPassword = $created['imap_client_password'] ?? null;
    $smtpPassword = $created['smtp_client_password'] ?? null;
    if (! is_string($id) || ! is_string($imapPassword) || ! is_string($smtpPassword)) {
        throw new RuntimeException('Client credentials missing.');
    }
    $connectionPath = "/email/mailbox-accounts/{$accountId}/connections/{$id}";
    [$status, $health] = gatewayRequest('POST', $connectionPath.'/test', $accountHeaders);
    expectStatus($status, 200, "{$label} connection probe");
    if (($health['health'] ?? null) !== 'healthy') { throw new RuntimeException("{$label} connection probe failed."); }
    [$status, $enabled] = gatewayRequest('PATCH', $connectionPath, $accountHeaders, ['enabled' => true]);
    expectStatus($status, 200, "{$label} connection enablement");
    if (($enabled['enabled'] ?? null) !== true) { throw new RuntimeException("{$label} connection is not enabled."); }
    [$status] = gatewayRequest('GET', "/_internal/gateway/mailbox-connections/{$id}/smtp",
        ['X-Gateway-Internal: '.$tokens['GATEWAY_INTERNAL_TOKEN']], null, 'http://127.0.0.1:5053');
    expectStatus($status, 200, "{$label} private SMTP credential resolution");
    [$status] = gatewayRequest('GET', "/_internal/gateway/mailbox-connections/{$id}/smtp");
    expectStatus($status, 404, "{$label} public credential isolation");
    $socket = loginToGatewayIMAP($id, $imapPassword);
    fwrite($socket, "a2 LOGOUT\r\n");
    fclose($socket);
    $connections[] = compact('accountId', 'accountToken', 'id', 'smtpPassword', 'address');
}

[$status] = gatewayRequest('GET', "/email/mailbox-accounts/{$connections[1]['accountId']}/connections/{$connections[1]['id']}",
    ['Authorization: Bearer '.$connections[0]['accountToken']]);
expectStatus($status, 401, 'cross-account credential isolation');

[$status] = gatewayRequest('GET', '/deliveries?limit=1', $operator);
expectStatus($status, 200, 'Planner-to-Spinner delivery-admin lookup');

foreach ($connections as $index => $connection) {
    $mime = "From: {$connection['address']}\r\nTo: recipient@example.test\r\nSubject: Bundle smoke {$suffix} {$index}\r\n\r\nHello.\r\n";
    $headers = ['Authorization: Basic '.base64_encode($connection['id'].':'.$connection['smtpPassword']),
        'Idempotency-Key: bundle-'.$suffix.'-'.$index];
    $payload = ['mail_from' => $connection['address'], 'recipients' => ['recipient@example.test'],
        'message_base64' => base64_encode($mime)];
    [$status, $submitted] = gatewayRequest('POST', '/email/submissions', $headers, $payload);
    expectStatus($status, 202, "account {$index} HTTPS submission");
    [$status, $duplicate] = gatewayRequest('POST', '/email/submissions', $headers, $payload);
    expectStatus($status, 202, "account {$index} idempotent replay");
    if (($submitted['id'] ?? null) !== ($duplicate['id'] ?? null)) {
        throw new RuntimeException('Idempotent submission returned a different receipt.');
    }
    $deadline = microtime(true) + 20;
    do {
        [$status, $receipt] = gatewayRequest('GET', '/deliveries/'.$submitted['id'], $operator);
        expectStatus($status, 200, "account {$index} delivery receipt");
        if (($receipt['state'] ?? null) === 'delivered') { break; }
        usleep(200_000);
    } while (microtime(true) < $deadline);
    if (($receipt['state'] ?? null) !== 'delivered') {
        throw new RuntimeException("Account {$index} delivery state is ".($receipt['state'] ?? 'unknown').
            ' after '.($receipt['attempts'] ?? 'unknown').' attempts.');
    }
}

echo "Bundled Mail: two isolated Planner connections, TLS IMAP, private SMTP lookup, HTTPS submission, idempotency, and delivered receipts passed.\n";
