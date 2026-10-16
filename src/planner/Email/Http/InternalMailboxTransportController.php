<?php

declare(strict_types=1);

namespace Webong\Gateway\Email\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Throwable;
use Webong\Gateway\Email\MailboxConnectionStore;
use Webong\Gateway\Email\EmailUpstreamCatalog;
use Webong\Gateway\Reconciliation\InternalRequestAuthenticator;

/** Private, short-lived credential resolution for one queued SMTP attempt. */
final readonly class InternalMailboxTransportController
{
    public function __construct(private InternalRequestAuthenticator $auth,
        private MailboxConnectionStore $connections, private EmailUpstreamCatalog $upstreams) {}

    public function __invoke(Request $request, string $connection): JsonResponse
    {
        $this->auth->authorizeOrHide($request);
        try { $resolved = $this->connections->resolve($connection, 'smtp'); }
        catch (Throwable) { return response()->json(['message' => 'Mailbox credential unavailable.'], 503)->header('Cache-Control', 'no-store'); }
        if ($resolved === null) { return response()->json(['message' => 'Mailbox connection unavailable.'], 404); }
        $upstream = $this->upstreams->active('smtp', $resolved['upstream_id']);
        if ($upstream === null) { return response()->json(['message' => 'Mailbox upstream unavailable.'], 404)->header('Cache-Control', 'no-store'); }

        return response()->json([
            'upstream_id' => $resolved['upstream_id'], 'username' => $resolved['username'],
            'secret' => $resolved['secret'], 'mechanism' => $resolved['mechanism'],
            'address' => $resolved['address'],
            'upstream_address' => $upstream['address'], 'upstream_server_name' => $upstream['server_name'],
            'upstream_tls_mode' => $upstream['tls_mode'],
        ])->header('Cache-Control', 'no-store');
    }
}
