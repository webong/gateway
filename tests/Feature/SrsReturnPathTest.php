<?php

declare(strict_types=1);

use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Schema;
use Illuminate\Database\QueryException;
use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Email\SrsCodec;
use Webong\Gateway\Email\TxtResolver;
use Webong\Gateway\Protocols\EventKind;
use Webong\Gateway\Protocols\GatewayAction;
use Webong\Gateway\Protocols\GatewayEvent;
use Webong\Gateway\Protocols\Protocol;

final class SrsTestTxtResolver implements TxtResolver
{
    public string $challenge = '';

    public function records(string $name): array
    {
        return str_starts_with($name, '_gateway-verify.') ? [$this->challenge] : [];
    }
}

it('rewrites only the outbound reverse path and routes a validated null-sender bounce', function (): void {
    $srs = $this->withToken('registry-secret')->postJson('/email/srs-policy', [
        'domain' => 'returns.example.test', 'keys' => [base64_encode(str_repeat('k', 32))],
    ])->assertCreated();
    expect($srs->json())->not->toHaveKey('keys');
    $txt = new SrsTestTxtResolver();
    $this->app->instance(TxtResolver::class, $txt);

    $returns = $this->withToken('registry-secret')->postJson('/email/domains', ['domain' => 'returns.example.test'])->assertCreated()->json();
    $txt->challenge = $returns['verification']['value'];
    $this->withToken('registry-secret')->patchJson('/email/domains/'.$returns['id'], ['enabled' => true])->assertOk();
    $this->withToken('registry-secret')->postJson('/email/domains/'.$returns['id'].'/aliases', [
        'local_part' => '*', 'recipients' => ['catchall@example.net'],
    ])->assertCreated();

    $planner = app(ProtocolPlanner::class);
    $message = "From: Sender <sender@origin.test>\r\nSubject: hello\r\n\r\nbody\r\n";
    $forward = new GatewayEvent(
        id: 'srs-forward', protocol: Protocol::SMTP, kind: EventKind::TRANSACTION,
        route: 'person@returns.example.test',
        attributes: ['recipients' => 'person@returns.example.test', 'mail_from' => 'sender@origin.test'],
        payload: $message,
    );
    $decision = $planner->planEvent($forward);
    $address = $decision->deliveries[0]->attributes['mail_from'];
    expect($decision->action)->toBe(GatewayAction::SUBMIT)
        ->and($address)->toStartWith('SRS0-')
        ->and($address)->toEndWith('@returns.example.test')
        ->and(strlen(explode('@', $address)[0]))->toBeLessThanOrEqual(64)
        ->and($decision->deliveries[0]->payload)->toBe($message)
        ->and(DB::table('srs_returns')->count())->toBe(1)
        ->and(DB::table('srs_returns')->value('original_sender'))->not->toContain('sender@origin.test');

    $recipient = static fn (string $to, array $attributes = []): GatewayEvent => new GatewayEvent(
        id: 'srs-rcpt', protocol: Protocol::SMTP, kind: EventKind::RECIPIENT,
        route: $to, attributes: $attributes,
    );
    expect($planner->planEvent($recipient($address))->action)->toBe(GatewayAction::ACCEPT)
        ->and($planner->planEvent($recipient($address, ['mail_from' => 'forged@origin.test']))->action)->toBe(GatewayAction::REJECT)
        ->and($planner->planEvent($recipient('person@returns.example.test', ['accepted_recipients' => json_encode([$address])]))->action)->toBe(GatewayAction::REJECT);

    $bounce = new GatewayEvent(
        id: 'srs-bounce', protocol: Protocol::SMTP, kind: EventKind::TRANSACTION,
        route: $address, attributes: ['recipients' => $address], payload: "Subject: delivery failed\r\n\r\nnotice\r\n",
    );
    $returned = app(ProtocolPlanner::class)->planEvent($bounce);
    expect($returned->action)->toBe(GatewayAction::SUBMIT)
        ->and($returned->deliveries[0]->attributes['mail_from'])->toBe('')
        ->and(json_decode($returned->deliveries[0]->attributes['recipients'], true))->toBe(['sender@origin.test']);

    $this->withToken('registry-secret')->withHeaders(['If-Match' => $srs->headers->get('ETag')])
        ->putJson('/email/srs-policy', ['rewrite_enabled' => false])->assertOk();
    expect(app(ProtocolPlanner::class)->planEvent($forward)->deliveries[0]->attributes['mail_from'])->toBe('sender@origin.test')
        ->and(app(ProtocolPlanner::class)->planEvent($bounce)->action)->toBe(GatewayAction::SUBMIT)
        ->and(DB::table('srs_returns')->count())->toBe(1);

    $tampered = substr_replace($address, $address[20] === 'a' ? 'b' : 'a', 20, 1);
    expect($planner->planEvent($recipient($tampered))->action)->toBe(GatewayAction::REJECT)
        ->and($planner->planEvent($recipient($address, ['accepted_recipients' => '["person@returns.example.test"]']))->action)->toBe(GatewayAction::REJECT)
        ->and($planner->planEvent(new GatewayEvent(
            id: 'srs-mixed', protocol: Protocol::SMTP, kind: EventKind::TRANSACTION,
            route: $address, attributes: ['recipients' => $address.',person@returns.example.test'], payload: $message,
        ))->action)->toBe(GatewayAction::REJECT);

    DB::table('srs_returns')->update(['expires_at' => now()->subSecond()]);
    expect($planner->planEvent($recipient($address))->action)->toBe(GatewayAction::REJECT);

    // A storage outage is not mistaken for an invalid token. The exception
    // crosses the protocol bridge, whose SMTP recipient path returns 451.
    Schema::drop('srs_returns');
    expect(fn (): mixed => $planner->planEvent($recipient($address)))->toThrow(QueryException::class);
});

it('reserves SRS-looking addresses ahead of managed catch-all aliases even when rewriting is disabled', function (): void {
    $txt = new SrsTestTxtResolver();
    $this->app->instance(TxtResolver::class, $txt);
    $domain = $this->withToken('registry-secret')->postJson('/email/domains', ['domain' => 'reserved.test'])->assertCreated()->json();
    $txt->challenge = $domain['verification']['value'];
    $this->withToken('registry-secret')->patchJson('/email/domains/'.$domain['id'], ['enabled' => true])->assertOk();
    $this->withToken('registry-secret')->postJson('/email/domains/'.$domain['id'].'/aliases', [
        'local_part' => '*', 'recipients' => ['catchall@example.net'],
    ])->assertCreated();
    $this->withToken('registry-secret')->postJson('/email/domains/'.$domain['id'].'/aliases', [
        'local_part' => 'SRS0-forged', 'recipients' => ['catchall@example.net'],
    ])->assertUnprocessable();

    $event = new GatewayEvent(
        id: 'reserved', protocol: Protocol::SMTP, kind: EventKind::RECIPIENT,
        route: 'SRS0-forged@reserved.test',
    );
    expect(app(ProtocolPlanner::class)->planEvent($event)->action)->toBe(GatewayAction::REJECT);
});

it('validates token age, domain, signature, and rotated keys', function (): void {
    $old = new SrsCodec('returns.example.test', [str_repeat('o', 32)]);
    $issued = $old->issue(time() - 3600);
    $rotated = new SrsCodec('returns.example.test', [str_repeat('n', 32), str_repeat('o', 32)]);
    $other = new SrsCodec('other.example.test', [str_repeat('o', 32)]);
    expect($rotated->verify($issued->address)?->hash)->toBe($issued->hash)
        ->and($other->verify($issued->address))->toBeNull()
        ->and((new SrsCodec('returns.example.test', [str_repeat('n', 32)]))->verify($issued->address))->toBeNull()
        ->and($rotated->verify($issued->address, time() + 22 * 86400))->toBeNull();
});
