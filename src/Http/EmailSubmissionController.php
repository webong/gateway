<?php

declare(strict_types=1);

namespace Webong\Gateway\Email\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Response;
use Throwable;
use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Deliveries\DeliveryAdminClient;
use Webong\Gateway\Email\EmailHttpSubmissionStore;
use Webong\Gateway\Email\EmailSubmissionKeyConflict;
use Webong\Gateway\Email\EmailSubmissionLimitExceeded;
use Webong\Gateway\Email\EmailRecipientSuppressed;
use Webong\Gateway\Email\EmailRuntimePolicyStore;
use Webong\Gateway\Email\MailboxConnectionStore;
use Webong\Gateway\Email\AccountReceivingStore;
use Webong\Gateway\Email\SharedSenderMessage;
use Webong\Gateway\Email\OutboundTransportStore;
use InvalidArgumentException;
use Webong\Gateway\Protocols\EventKind;
use Webong\Gateway\Protocols\GatewayAction;
use Webong\Gateway\Protocols\GatewayEvent;
use Webong\Gateway\Protocols\Protocol;

/** Public credential-authenticated submission; Spinner owns durable acceptance. */
final readonly class EmailSubmissionController
{
    private const MAX_MESSAGE_SIZE = 10 * 1024 * 1024;

    public function __construct(
        private ProtocolPlanner $planner,
        private EmailHttpSubmissionStore $submissions,
        private DeliveryAdminClient $admin,
        private MailboxConnectionStore $connections,
        private AccountReceivingStore $receiving,
        private \Webong\Gateway\Email\EmailSubmissionPolicyStore $policies,
        private EmailRuntimePolicyStore $runtimePolicy,
        private OutboundTransportStore $outbound,
    ) {}

    public function show(Request $request, string $delivery): JsonResponse
    {
        if (! $this->runtimePolicy->current()['http_submission_enabled']) {
            return response()->json(['message' => 'Not found.'], Response::HTTP_NOT_FOUND);
        }
        if (! $request->isSecure()) {
            return response()->json(['message' => 'HTTPS is required.'], Response::HTTP_FORBIDDEN);
        }
        $username = $request->getUser();
        $password = $request->getPassword();
        if (! is_string($username) || $username === '' || ! is_string($password) || $password === '') {
            return $this->unauthorized();
        }
        try {
            $decision = $this->planner->planEvent(new GatewayEvent(
                id: bin2hex(random_bytes(16)), protocol: Protocol::SMTP, kind: EventKind::AUTHENTICATE,
                route: $username, attributes: ['identity' => $username, 'username' => $username,
                    'password' => $password, 'mechanism' => 'HTTP-Basic', 'remote_addr' => (string) $request->ip()],
            ));
            $identity = $decision->metadata['auth_identity'] ?? null;
            if ($decision->action !== GatewayAction::ACCEPT || ! is_string($identity)) { return $this->unauthorized(); }
            $receipt = $this->submissions->findReceipt($identity, $delivery);
            if ($receipt === null) { return response()->json(['message' => 'Submission not found.'], 404); }
            $response = $this->admin->get('/deliveries/'.$delivery, [], $receipt->node);
            if (! $response->ok() || ! is_array($response->json()) || $response->json('id') !== $delivery) {
                return response()->json(['message' => 'Submission status is unavailable.'], 503);
            }
            $queue = $response->json();
            $attempt = $this->outbound->attempt($delivery);
            $state = match ($queue['state'] ?? null) {
                'queued' => 'queued', 'deferred' => 'temporarily_delayed',
                'delivered' => 'accepted',
                'failed' => ($queue['body_assertion'] ?? null) === 'smtp_acceptance_unknown'
                    ? 'ambiguous' : 'rejected',
                default => null,
            };
            if ($state === null) { return response()->json(['message' => 'Submission status is unavailable.'], 503); }
            if ($attempt !== null && str_starts_with($identity, $attempt->document['account_id'].':')) {
                $observed = $attempt->document['state'];
                if (in_array($observed, ['delivered', 'bounced', 'complained', 'ambiguous'], true)) {
                    $state = $observed;
                } elseif (in_array($state, ['queued', 'temporarily_delayed'], true)
                    && in_array($observed, ['attempted', 'temporarily_delayed'], true)) {
                    $state = $observed;
                }
            }
            return response()->json(['id' => $delivery, 'state' => $state,
                'attempts' => $queue['attempts'] ?? 0])->header('Cache-Control', 'no-store');
        } catch (Throwable) {
            return response()->json(['message' => 'Submission status is unavailable.'], 503);
        }
    }

    public function __invoke(Request $request): JsonResponse
    {
        if (! $this->runtimePolicy->current()['http_submission_enabled']) {
            return response()->json(['message' => 'Not found.'], Response::HTTP_NOT_FOUND);
        }
        if (! $request->isSecure()) {
            return response()->json(['message' => 'HTTPS is required.'], Response::HTTP_FORBIDDEN);
        }
        $username = $request->getUser();
        $password = $request->getPassword();
        if (! is_string($username) || $username === '' || ! is_string($password) || $password === '') {
            return $this->unauthorized();
        }
        $key = $request->header('Idempotency-Key');
        if (! is_string($key) || preg_match('/\A[a-zA-Z0-9._:-]{1,128}\z/D', $key) !== 1) {
            return response()->json(['message' => 'A valid Idempotency-Key header is required.'], Response::HTTP_UNPROCESSABLE_ENTITY);
        }
        if (! $request->isJson() || strlen($request->getContent()) > self::MAX_MESSAGE_SIZE * 2 + 65536) {
            return response()->json(['message' => 'A bounded JSON email submission is required.'], Response::HTTP_BAD_REQUEST);
        }
        $input = $request->json()->all();
        if (array_diff(array_keys($input), ['mail_from', 'recipients', 'message_base64', 'sender_name', 'reply_route_id']) !== []
            || ! is_string($input['mail_from'] ?? null)
            || ! is_array($input['recipients'] ?? null) || ! array_is_list($input['recipients'])
            || count($input['recipients']) < 1 || count($input['recipients']) > 100
            || count(array_filter($input['recipients'], 'is_string')) !== count($input['recipients'])
            || ! is_string($input['message_base64'] ?? null)
            || ($message = base64_decode($input['message_base64'], true)) === false
            || $message === '' || strlen($message) > self::MAX_MESSAGE_SIZE) {
            return response()->json(['message' => 'Invalid email submission.'], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        try {
            $decision = $this->planner->planEvent(new GatewayEvent(
                id: bin2hex(random_bytes(16)), protocol: Protocol::SMTP, kind: EventKind::AUTHENTICATE,
                route: $username,
                attributes: [
                    'identity' => $username, 'username' => $username, 'password' => $password,
                    'mechanism' => 'HTTP-Basic', 'remote_addr' => (string) $request->ip(),
                ],
            ));
        } catch (Throwable) {
            return response()->json(['message' => 'Email authentication is unavailable.'], Response::HTTP_SERVICE_UNAVAILABLE);
        }
        if ($decision->protocol !== Protocol::SMTP || $decision->action !== GatewayAction::ACCEPT) {
            return $this->unauthorized();
        }
        $identity = $decision->metadata['auth_identity'] ?? '';
        $rawSenders = $decision->metadata['authorized_mail_from'] ?? '';
        $senders = is_string($rawSenders) ? json_decode($rawSenders, true) : null;
        if (! is_string($identity) || $identity === '' || strlen($identity) > 255 || trim($identity) !== $identity
            || preg_match('/\p{Cc}/u', $identity) !== 0
            || ! is_array($senders) || ! array_is_list($senders) || $senders === [] || count($senders) > 100
            || count(array_filter($senders, 'is_string')) !== count($senders)
            || ! in_array($input['mail_from'], $senders, true)) {
            return $this->unauthorized();
        }
        $connectionId = $decision->metadata['mailbox_connection_id'] ?? '';
        $sharedRouteId = $decision->metadata['shared_sender_route_id'] ?? '';
        if (! is_string($sharedRouteId)) { return $this->unauthorized(); }
        if ($sharedRouteId !== '') {
            $separator = strrpos($identity, ':');
            $account = $separator === false ? '' : substr($identity, 0, $separator);
            $replyRoute = is_string($input['reply_route_id'] ?? null)
                ? $this->receiving->route($account, $input['reply_route_id']) : null;
            if ($identity !== $account.':'.$sharedRouteId || $replyRoute === null
                || ! $replyRoute->enabled || ($replyRoute->document['mode'] ?? null) !== 'forward'
                || ! is_string($input['sender_name'] ?? null)) {
                return response()->json(['message' => 'Invalid shared sender reply route.'], Response::HTTP_UNPROCESSABLE_ENTITY);
            }
            try {
                if ($this->receiving->activeAddress($replyRoute->key) === null) {
                    return response()->json(['message' => 'Shared sender reply route is unavailable.'], Response::HTTP_CONFLICT);
                }
                $message = SharedSenderMessage::rewrite($message, $input['sender_name'],
                    $input['mail_from'], $replyRoute->key);
            } catch (InvalidArgumentException) {
                return response()->json(['message' => 'Invalid shared sender message.'], Response::HTTP_UNPROCESSABLE_ENTITY);
            } catch (Throwable) {
                return response()->json(['message' => 'Shared sender reply route is unavailable.'], Response::HTTP_SERVICE_UNAVAILABLE);
            }
        } elseif (isset($input['sender_name']) || isset($input['reply_route_id'])) {
            return response()->json(['message' => 'Shared sender fields require a shared route.'], Response::HTTP_UNPROCESSABLE_ENTITY);
        }
        if (! is_string($connectionId) || ($connectionId !== '' && ! $this->runtimePolicy->current()['connected_smtp_enabled'])) {
            return response()->json(['message' => 'Mailbox SMTP routing is unavailable.'], Response::HTTP_SERVICE_UNAVAILABLE);
        }
        if ($connectionId !== '') {
            $separator = strrpos($identity, ':');
            $connection = $separator === false ? null : $this->connections->find(substr($identity, 0, $separator), $connectionId);
            if ($connection === null || ! $connection->enabled || $identity !== $connection->account_id.':'.$connectionId
                || ! hash_equals($connection->address, $input['mail_from'])) {
                return $this->unauthorized();
            }
        }
        $identitySeparator = strrpos($identity, ':');
        $outboundBinding = $connectionId === '' && $identitySeparator !== false
            ? $this->outbound->senderBinding(substr($identity, 0, $identitySeparator),
                substr($identity, $identitySeparator + 1)) : null;
        $rawLimit = $decision->metadata['submission_daily_limit'] ?? '';
        $rawByteLimit = $decision->metadata['submission_daily_byte_limit'] ?? '';
        $policy = $this->policies->limits();
        $limit = $this->limit($rawLimit, $policy['daily_limit'], 1_000_000);
        $byteLimit = $this->limit($rawByteLimit, $policy['daily_byte_limit'], 1_000_000_000_000);
        if ($limit === null || $byteLimit === null) {
            return response()->json(['message' => 'Email submission quota policy is invalid.'], Response::HTTP_SERVICE_UNAVAILABLE);
        }
        try {
            $nodes = $this->admin->nodes();
            $fingerprint = hash('sha256', json_encode([$connectionId, $sharedRouteId, $input['mail_from'], $input['recipients']], JSON_THROW_ON_ERROR)."\0".$message);
            $reservation = $this->submissions->reserve($identity, $input['mail_from'], $input['recipients'], $key, $fingerprint, $nodes, $limit, strlen($message), $byteLimit);
            $node = $reservation->node;
            $id = $reservation->receipt_id;
            if ($reservation->accepted_at !== null) {
                return $this->accepted($id, $node);
            }
            $response = $this->admin->postJson('/email/submissions', [
                'receipt_id' => $id,
                'mail_from' => $input['mail_from'],
                'authorized_senders' => $senders,
                'recipients' => $input['recipients'],
                'message' => base64_encode($message),
                ...($connectionId !== '' ? ['mailbox_connection_id' => $connectionId] : []),
                ...($outboundBinding !== null ? ['outbound_binding' => $outboundBinding] : []),
            ], $node);
        } catch (EmailSubmissionKeyConflict) {
            return response()->json(['message' => 'Idempotency key already belongs to a different message.'], Response::HTTP_CONFLICT);
        } catch (EmailSubmissionLimitExceeded) {
            return response()->json(['message' => 'Daily submission quota exceeded.'], Response::HTTP_TOO_MANY_REQUESTS);
        } catch (EmailRecipientSuppressed) {
            return response()->json(['message' => 'Recipient is suppressed.'], Response::HTTP_UNPROCESSABLE_ENTITY);
        } catch (Throwable) {
            return response()->json(['message' => 'Email submission is unavailable.'], Response::HTTP_SERVICE_UNAVAILABLE);
        }
        if ($response->status() === Response::HTTP_CONFLICT) {
            return response()->json(['message' => 'Email submission receipt conflicts with another message.'], Response::HTTP_CONFLICT);
        }
        if ($response->status() === Response::HTTP_UNPROCESSABLE_ENTITY || $response->status() === Response::HTTP_REQUEST_ENTITY_TOO_LARGE) {
            return response()->json(['message' => 'Email submission was rejected.'], Response::HTTP_UNPROCESSABLE_ENTITY);
        }
        if ($response->status() !== Response::HTTP_ACCEPTED || $response->json('id') !== $id) {
            return response()->json(['message' => 'Email submission is unavailable.'], Response::HTTP_SERVICE_UNAVAILABLE);
        }

        try {
            $this->submissions->markAccepted($identity, $key, $id);
        } catch (Throwable) {
            // Spinner has already durably accepted the message. A retry still
            // uses the pinned receipt ID and cannot enqueue it a second time.
        }

        return $this->accepted($id, $node);
    }

    private function accepted(string $id, string $node): JsonResponse
    {
        return response()->json(['id' => $id, 'state' => 'queued', 'node' => $node,
            'delivery_url' => '/deliveries/'.$id, 'status_url' => '/email/submissions/'.$id], Response::HTTP_ACCEPTED)
            ->header('Cache-Control', 'no-store');
    }

    private function unauthorized(): JsonResponse
    {
        return response()->json(['message' => 'Email authentication failed.'], Response::HTTP_UNAUTHORIZED)
            ->header('WWW-Authenticate', 'Basic realm="Gateway Email"');
    }

    private function limit(mixed $override, int $default, int $maximum): ?int
    {
        if ($default < 0 || $default > $maximum || ! is_string($override)) {
            return null;
        }
        if ($override === '') {
            return $default;
        }

        return ctype_digit($override) && (int) $override >= 1 && (int) $override <= $maximum
            ? (int) $override : null;
    }
}
