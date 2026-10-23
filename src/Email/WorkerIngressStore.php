<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use stdClass;
use Webong\Gateway\Resources\PlannerResourceStore;

/** Dedicated Worker bearer authority for one account-owned receiving route. */
final readonly class WorkerIngressStore
{
    private const KIND = 'email.worker-ingress';

    public function __construct(private PlannerResourceStore $resources, private AccountReceivingStore $receiving) {}

    public function find(string $account, string $route): ?stdClass
    {
        return $this->resources->byOwnedKey(self::KIND, $account, $route);
    }

    public function delete(string $account, string $route): void
    {
        $row = $this->find($account, $route);
        if ($row !== null) {
            $this->resources->delete(self::KIND, $row->id, $row->revision, $account);
        }
    }

    /** Rotating the token invalidates a previously deployed Worker secret. */
    public function rotate(string $account, string $route): ?array
    {
        $receiving = $this->receiving->route($account, $route);
        if ($receiving === null || $this->receiving->ingressAdapter($receiving) !== 'worker') { return null; }
        $token = bin2hex(random_bytes(32));
        $document = ['route_id' => $route, 'token_hash' => hash('sha256', $token)];
        $current = $this->find($account, $route);
        $row = $current === null
            ? $this->resources->create(self::KIND, $route, $document, owner: $account)
            : $this->resources->replace(self::KIND, $current->id, $route, $document, true, $current->revision, $account);
        return ['record' => $row, 'token' => $token];
    }

    /** @return array<string, mixed>|null */
    public function authorize(string $routeID, string $token, string $mailFrom, string $recipient,
        string $eventID, string $messageHash): ?array
    {
        if (strlen($token) !== 64 || preg_match('/\A[a-f0-9]{64}\z/D', $token) !== 1
            || preg_match('/\A[a-f0-9]{64}\z/D', $eventID) !== 1
            || preg_match('/\A[a-f0-9]{64}\z/D', $messageHash) !== 1
            || strlen($mailFrom) > 320 || ($mailFrom !== '' && filter_var($mailFrom, FILTER_VALIDATE_EMAIL) === false)
            || filter_var($recipient, FILTER_VALIDATE_EMAIL) === false) { return null; }
        $route = $this->resources->find('email.receiving-route', $routeID);
        if ($route === null || ! $route->enabled || $this->receiving->ingressAdapter($route) !== 'worker'
            || ! hash_equals($route->key, strtolower($recipient))) { return null; }
        $credential = $this->find($route->document['account_id'], $routeID);
        if ($credential === null || ! $credential->enabled
            || ! hash_equals($credential->document['token_hash'], hash('sha256', $token))) { return null; }
        if ($this->receiving->activeAddress($route->key) === null) { return null; }
        $envelope = base64_encode(json_encode(['mail_from' => $mailFrom, 'recipients' => [$route->key]], JSON_THROW_ON_ERROR));
        $signed = $routeID."\n".$eventID."\n".$envelope."\n".$messageHash;
        $private = $this->receiving->privateCallbackCidrs($route);
        return ['callback_url' => $route->document['callback_url'], 'headers' => [
            'Content-Type' => ['message/rfc822'], 'X-Gateway-Route-Id' => [$routeID],
            'X-Gateway-Event-Id' => [$eventID], 'X-Gateway-Envelope' => [$envelope],
            'X-Gateway-Signature' => ['sha256='.hash_hmac('sha256', $signed, $route->document['webhook_secret'])],
        ], 'private_callback_cidrs' => $private];
    }
}
