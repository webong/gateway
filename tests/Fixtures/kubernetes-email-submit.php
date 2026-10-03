<?php

declare(strict_types=1);

$message = "From: sender@example.test\r\nSubject: kubernetes HTTP submission\r\n\r\nGateway shared queue payload\r\n";
$body = json_encode([
    'mail_from' => 'sender@example.test',
    'recipients' => ['recipient@example.test'],
    'message_base64' => base64_encode($message),
], JSON_THROW_ON_ERROR);
$context = stream_context_create([
    'http' => [
        'method' => 'POST',
        'header' => 'Authorization: Basic ' . base64_encode('smoke-sender:smoke-secret') . "\r\n"
            . "Idempotency-Key: kubernetes-email-1\r\n"
            . "X-Forwarded-Proto: https\r\n"
            . "Content-Type: application/json\r\n",
        'content' => $body,
        'ignore_errors' => true,
        'timeout' => 8,
    ]
]);
$response = file_get_contents('http://127.0.0.1:8080/email/submissions', false, $context);
$status = $http_response_header[0] ?? '';
if ($response === false || !preg_match('/\s202\s/', $status)) {
    fwrite(STDERR, "Email HTTP submission failed: {$status} {$response}\n");
    exit(1);
}
$receipt = json_decode($response, true, 512, JSON_THROW_ON_ERROR);
$id = $receipt['id'] ?? null;
if (!is_string($id) || preg_match('/\A[a-f0-9]{32}\z/', $id) !== 1) {
    throw new RuntimeException('Email HTTP submission returned no receipt ID.');
}

echo $id;
