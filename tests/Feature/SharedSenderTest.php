<?php

declare(strict_types=1);

use Illuminate\Http\Client\Request as ClientRequest;
use Illuminate\Support\Facades\Http;
use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Email\EmailDkimKeyStore;
use Webong\Gateway\Email\EmailRuntimePolicyStore;
use Webong\Gateway\Email\ManagedSendingStore;
use Webong\Gateway\Email\SharedSenderMessage;
use Webong\Gateway\Email\TxtResolver;
use Webong\Gateway\Protocols\EventKind;
use Webong\Gateway\Protocols\GatewayAction;
use Webong\Gateway\Protocols\GatewayEvent;
use Webong\Gateway\Protocols\Protocol;

it('sends from the configured shared identity and routes replies by conversation', function (): void {
    config()->set('gateway.email.shared_from_address', 'support@operator.test');
    config()->set('gateway.email.managed_dkim_signing_enabled', true);
    app(EmailDkimKeyStore::class)->ensureGenerated('operator.test', 'gateway1');
    $dns = new class implements TxtResolver {
        public string $token = '';
        public function records(string $name): array {
            $record = app(EmailDkimKeyStore::class)->publicRecord('operator.test');
            if ($record !== null && $name === $record['name']) { return [$record['value']]; }
            return $this->token === '' ? [] : ['gateway-verification='.$this->token];
        }
    };
    app()->instance(TxtResolver::class, $dns);
    $forward = $this->withToken('registry-secret')->postJson('/email/domains', ['domain' => 'inbound.operator.test'])->assertCreated()->json();
    $dns->token = substr($forward['verification']['value'], strlen('gateway-verification='));
    $this->withToken('registry-secret')->postJson('/email/domains/'.$forward['id'].'/verify')->assertOk();
    $this->withToken('registry-secret')->patchJson('/email/domains/'.$forward['id'], ['enabled' => true])->assertOk();
    $this->withToken('registry-secret')->putJson('https://localhost/email/receiving-settings', [
        'mx_host' => 'mx.operator.test', 'forwarding_domain' => 'inbound.operator.test',
    ])->assertOk();
    $one = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', ['id' => 'one'])->assertCreated()->json();
    $two = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', ['id' => 'two'])->assertCreated()->json();
    $base = 'https://localhost/email/mailbox-accounts/one';
    $reply = [];
    foreach (['conversation-1', 'conversation-2'] as $conversation) {
        $reply[] = $this->withToken($one['token'])->postJson($base.'/receiving-routes', [
            'mode' => 'forward', 'external_id' => $conversation, 'webhook_secret' => str_repeat('a', 64),
            'callback_url' => 'https://zorvia.example.test/inbound',
        ])->assertCreated()->json();
    }
    expect($reply[0]['address'])->not->toBe($reply[1]['address']);
    foreach ($reply as $index => $route) {
        $inbound = app(ProtocolPlanner::class)->planEvent(new GatewayEvent(
            id: 'conversation-reply-'.$index, protocol: Protocol::SMTP, kind: EventKind::TRANSACTION,
            route: $route['address'], attributes: ['mail_from' => 'buyer@example.test',
                'recipients' => $route['address']], payload: "From: buyer@example.test\r\n\r\nreply"));
        expect($inbound->action)->toBe(GatewayAction::DELIVER)
            ->and($inbound->deliveries[0]->attributes['route_id'])->toBe($route['id']);
    }
    $sender = $this->withToken($one['token'])->postJson($base.'/sending-routes', ['mode' => 'shared'])
        ->assertCreated()->assertJsonPath('address', 'support@operator.test')->json();
    $this->withToken($two['token'])->getJson('https://localhost/email/mailbox-accounts/two/sending-routes/'.$sender['id'])->assertNotFound();
    $auth = ['Authorization' => 'Basic '.base64_encode($sender['id'].':'.$sender['smtp_client_password']),
        'Idempotency-Key' => 'conversation-send-1'];
    $raw = "From: contact@customer.test\r\nReply-To: old@customer.test\r\nSubject: Hello\r\n\r\nbody";
    $payload = ['mail_from' => 'support@operator.test', 'recipients' => ['buyer@example.test'],
        'sender_name' => 'Customer Support', 'reply_route_id' => $reply[0]['id'],
        'message_base64' => base64_encode($raw)];
    app(EmailRuntimePolicyStore::class)->create(['http_submission_enabled' => true]);
    config()->set('gateway.delivery_admin_url', 'http://spinner.internal:5052');
    Http::fake(fn (ClientRequest $request) => Http::response(['id' => $request['receipt_id']], 202));
    $this->withHeaders($auth)->postJson('https://localhost/email/submissions', $payload)->assertAccepted();
    Http::assertSent(function (ClientRequest $request) use ($reply): bool {
        $mime = base64_decode($request['message'], true);
        return $request['mail_from'] === 'support@operator.test'
            && str_contains($mime, 'From: "Customer Support" <support@operator.test>')
            && str_contains($mime, 'Reply-To: <'.$reply[0]['address'].'>')
            && ! str_contains($mime, 'old@customer.test');
    });
    $this->withHeaders([...$auth, 'Idempotency-Key' => 'conversation-send-2'])
        ->postJson('https://localhost/email/submissions', [...$payload, 'reply_route_id' => $reply[1]['id']])->assertAccepted();
    $this->withHeaders([...$auth, 'Idempotency-Key' => 'conversation-send-3'])
        ->postJson('https://localhost/email/submissions', [...$payload, 'reply_route_id' => 'unknown'])->assertUnprocessable();
    $otherReply = $this->withToken($two['token'])->postJson('https://localhost/email/mailbox-accounts/two/receiving-routes', [
        'mode' => 'forward', 'external_id' => 'foreign-conversation', 'webhook_secret' => str_repeat('b', 64),
        'callback_url' => 'https://zorvia.example.test/inbound',
    ])->assertCreated()->json();
    $this->withHeaders([...$auth, 'Idempotency-Key' => 'conversation-send-4'])
        ->postJson('https://localhost/email/submissions', [...$payload, 'reply_route_id' => $otherReply['id']])->assertUnprocessable();
    $smtp = app(ProtocolPlanner::class)->planEvent(new GatewayEvent(id: 'shared-smtp', protocol: Protocol::SMTP,
        kind: EventKind::AUTHENTICATE, route: $sender['id'],
        attributes: ['username' => $sender['id'], 'password' => $sender['smtp_client_password'], 'mechanism' => 'PLAIN']));
    expect($smtp->action)->toBe(GatewayAction::REJECT);

    $transport = $this->withToken('registry-secret')->postJson('https://localhost/email/outbound-transports', [
        'name' => 'shared-smtp', 'type' => 'smtp', 'address' => 'smtp.operator.test:587',
        'tls_mode' => 'starttls', 'username' => 'operator', 'password' => 'private-password',
        'authorized_domains' => ['operator.test'], 'healthy' => true,
        'domain_readiness' => ['operator.test' => ['spf' => 'ready', 'dkim' => 'ready']],
    ])->assertCreated()->json();
    $pool = $this->withToken('registry-secret')->postJson('https://localhost/email/outbound-pools', [
        'name' => 'shared-pool', 'members' => [['transport_id' => $transport['id'], 'priority' => 0, 'weight' => 1]],
    ])->assertCreated()->json();
    $this->withToken('registry-secret')->postJson('https://localhost/email/outbound-bindings', [
        'account_id' => 'one', 'scope' => 'shared_route', 'sender_id' => $sender['id'], 'pool_id' => $pool['id'],
    ])->assertCreated();
    $this->withHeaders([...$auth, 'Idempotency-Key' => 'conversation-managed-pool'])
        ->postJson('https://localhost/email/submissions', $payload)->assertAccepted();
    Http::assertSent(function (ClientRequest $request) use ($pool, $reply): bool {
        $mime = base64_decode($request['message'], true);
        return ($request['outbound_binding']['pool_id'] ?? null) === $pool['id']
            && $request['mail_from'] === 'support@operator.test'
            && str_contains($mime, 'Reply-To: <'.$reply[0]['address'].'>');
    });
});

it('preserves MIME body bytes and rejects conflicting sender headers', function (): void {
    $mime = "From: old@example.test\r\nSubject: test\r\n\r\n\x00\xffbody";
    $rewritten = SharedSenderMessage::rewrite($mime, 'Support', 'support@operator.test', 'reply@inbound.operator.test');
    expect($rewritten)->toEndWith("\x00\xffbody");
    expect(SharedSenderMessage::rewrite($mime, 'Équipe Support', 'support@operator.test',
        'reply@inbound.operator.test'))->toContain('=?UTF-8?B?');
    expect(fn () => SharedSenderMessage::rewrite("From: old@example.test\r\nSender: bad@example.test\r\n\r\nbody",
        'Support', 'support@operator.test', 'reply@inbound.operator.test'))->toThrow(InvalidArgumentException::class);
});

it('rejects shared sender setup without ready signing', function (string $unavailable): void {
    config()->set('gateway.email.shared_from_address', 'support@operator.test');
    config()->set('gateway.email.managed_dkim_signing_enabled', $unavailable !== 'disabled signing');
    if ($unavailable !== 'missing key') {
        $keys = app(EmailDkimKeyStore::class);
        $key = $keys->ensureGenerated('operator.test', 'gateway1');
        if ($unavailable === 'disabled key') {
            $keys->replace($key->id, ['domain' => 'operator.test', 'selector' => 'gateway1', 'enabled' => false], $key->revision);
        }
    }
    app()->instance(TxtResolver::class, new class implements TxtResolver {
        public function records(string $name): array { return []; }
    });
    $account = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', ['id' => 'not-ready'])
        ->assertCreated()->json();

    $this->withToken($account['token'])->postJson('https://localhost/email/mailbox-accounts/not-ready/sending-routes', ['mode' => 'shared'])
        ->assertConflict()->assertJsonPath('message', 'Shared sender signing and DNS are not ready.');
    expect(app(ManagedSendingStore::class)->routes('not-ready'))->toBeEmpty();
})->with(['missing key', 'disabled key', 'missing DNS', 'disabled signing']);

it('marks an existing shared sender pending and rejects activation when its key disappears', function (): void {
    config()->set('gateway.email.shared_from_address', 'support@operator.test');
    config()->set('gateway.email.managed_dkim_signing_enabled', true);
    $keys = app(EmailDkimKeyStore::class);
    $key = $keys->ensureGenerated('operator.test', 'gateway1');
    $record = $keys->publicRecord('operator.test');
    $dns = new class implements TxtResolver {
        public array $records = [];
        public bool $unavailable = false;
        public function records(string $name): array {
            if ($this->unavailable) { throw new RuntimeException('DNS unavailable'); }
            return $this->records[$name] ?? [];
        }
    };
    $dns->records[$record['name']] = [$record['value']];
    app()->instance(TxtResolver::class, $dns);
    $account = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', ['id' => 'existing'])
        ->assertCreated()->json();
    $base = 'https://localhost/email/mailbox-accounts/existing/sending-routes';
    $route = $this->withToken($account['token'])->postJson($base, ['mode' => 'shared'])
        ->assertCreated()->assertJsonPath('status', 'active')->json();

    $dns->unavailable = true;
    expect(app(ManagedSendingStore::class)->authenticate($route['id'], $route['smtp_client_password']))->toBeNull();
    $dns->unavailable = false;
    $keys->delete($key->id, $key->revision);

    $this->withToken($account['token'])->getJson($base.'/'.$route['id'])->assertOk()->assertJsonPath('status', 'pending');
    $this->withToken($account['token'])->patchJson($base.'/'.$route['id'], ['enabled' => true])->assertConflict();
    expect(app(ManagedSendingStore::class)->authenticate($route['id'], $route['smtp_client_password']))->toBeNull();
});
