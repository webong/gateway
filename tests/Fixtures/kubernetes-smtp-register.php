<?php

declare(strict_types=1);

function registryRequest(string $method, string $path, ?array $payload = null): array
{
    $body = $payload === null ? null : json_encode($payload, JSON_THROW_ON_ERROR);
    $context = stream_context_create(['http' => [
        'method' => $method,
        'header' => 'Authorization: Bearer '.getenv('REGISTRY_TOKEN')."\r\nContent-Type: application/json\r\n",
        'content' => $body ?? '',
        'timeout' => 5,
    ]]);
    $response = file_get_contents('http://127.0.0.1:8080'.$path, false, $context);
    if ($response === false) {
        throw new RuntimeException("Registry request failed: {$method} {$path}");
    }

    return $response === '' ? [] : json_decode($response, true, 512, JSON_THROW_ON_ERROR);
}

$domain = registryRequest('POST', '/email/domains', ['domain' => 'managed.test']);
$id = $domain['id'] ?? null;
if (! is_string($id) || $id === '') {
    throw new RuntimeException('Email domain registration returned no ID.');
}

registryRequest('POST', "/email/domains/{$id}/verify");
registryRequest('PATCH', "/email/domains/{$id}", ['enabled' => true]);
registryRequest('POST', "/email/domains/{$id}/aliases", [
    'local_part' => 'first',
    'recipients' => ['one@example.net'],
]);

echo "Registered managed.test and first@managed.test.\n";
