<?php

declare(strict_types=1);

namespace Webong\Gateway\Email\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use RuntimeException;
use Throwable;
use Webong\Gateway\Accounts\AccountStore;
use Webong\Gateway\Email\MailboxOAuthAuthorization;

final readonly class MailboxOAuthController
{
    public function __construct(private AccountStore $accounts, private MailboxOAuthAuthorization $oauth) {}

    public function start(Request $request, string $account): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        try { return $this->ok($this->oauth->start($account, $request->json()->all()), 201); }
        catch (InvalidArgumentException) { return $this->error('Invalid mailbox authorization request.', 422); }
        catch (Throwable) { return $this->error('Mailbox provider is unavailable.', 503); }
    }

    public function complete(Request $request, string $account): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        try { return $this->ok($this->oauth->complete($account, $request->json()->all())); }
        catch (InvalidArgumentException) { return $this->error('Invalid mailbox authorization callback.', 422); }
        catch (RuntimeException) { return $this->error('Mailbox authorization could not be completed.', 409); }
        catch (Throwable) { return $this->error('Mailbox authorization could not be completed.', 409); }
    }

    private function authorize(Request $request, string $account): ?JsonResponse
    {
        if (! $request->isSecure()) { return $this->error('HTTPS is required.', 403); }
        $token = $request->bearerToken();
        return is_string($token) && $this->accounts->authorized($account, $token)
            ? null : $this->error('Unauthenticated.', 401);
    }

    private function ok(array $body, int $status = 200): JsonResponse
    {
        return response()->json($body, $status)->header('Cache-Control', 'no-store');
    }

    private function error(string $message, int $status): JsonResponse
    {
        return $this->ok(['message' => $message], $status);
    }
}
