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
use Webong\Gateway\Email\MailboxConnectionStore;
use Webong\Gateway\Protocol\EventKind;
use Webong\Gateway\Protocol\GatewayAction;
use Webong\Gateway\Protocol\GatewayEvent;
use Webong\Gateway\Protocol\Protocol;

/** Public credential-authenticated submission; Spinner owns durable acceptance. */
final readonly class EmailSubmissionController
{
    private const MAX_MESSAGE_SIZE = 10 * 1024 * 1024;

    public function __construct(
        private ProtocolPlanner $planner,
        private EmailHttpSubmissionStore $submissions,
        private DeliveryAdminClient $admin,
        private MailboxConnectionStore $connections,
    ) {}

    public function __invoke(Request $request): JsonResponse
    {
        if (! config('gateway.email.http_submission_enabled', false)) {
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
        if (array_diff(array_keys($input), ['mail_from', 'recipients', 'message_base64']) !== []
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
        if (! is_string($connectionId) || ($connectionId !== '' && ! config('gateway.email.connected_smtp_enabled', false))) {
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
        $rawLimit = $decision->metadata['submission_daily_limit'] ?? '';
        $rawByteLimit = $decision->metadata['submission_daily_byte_limit'] ?? '';
        $limit = $this->limit($rawLimit, (int) config('gateway.email.submission_daily_limit', 0), 1_000_000);
        $byteLimit = $this->limit($rawByteLimit, (int) config('gateway.email.submission_daily_byte_limit', 0), 1_000_000_000_000);
        if ($limit === null || $byteLimit === null) {
            return response()->json(['message' => 'Email submission quota policy is invalid.'], Response::HTTP_SERVICE_UNAVAILABLE);
        }
        try {
            $nodes = $this->admin->nodes();
            $fingerprint = hash('sha256', json_encode([$connectionId, $input['mail_from'], $input['recipients']], JSON_THROW_ON_ERROR)."\0".$message);
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
                'message' => $input['message_base64'],
                ...($connectionId !== '' ? ['mailbox_connection_id' => $connectionId] : []),
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
        return response()->json(['id' => $id, 'state' => 'queued', 'node' => $node, 'delivery_url' => '/deliveries/'.$id], Response::HTTP_ACCEPTED)
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
