<?php

declare(strict_types=1);

use Illuminate\Support\Facades\DB;
use Illuminate\Support\Carbon;
use Webong\Gateway\Email\TxtResolver;
use Webong\Gateway\Email\EmailMessagePolicy;
use Webong\Gateway\Email\EmailSubmissionAuthenticator;
use Webong\Gateway\Email\EmailSubmissionGrant;
use Webong\Gateway\Email\EmailSubmissionQuota;
use Webong\Gateway\Email\MailboxAuthenticator;
use Webong\Gateway\Email\MailboxAccessGrant;
use Webong\Gateway\Email\EmailPolicyVerdict;
use Webong\Gateway\Email\DmarcAssessment;
use Webong\Gateway\Email\VerificationRecordMissing;
use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Protocol\EventKind;
use Webong\Gateway\Protocol\GatewayAction;
use Webong\Gateway\Protocol\GatewayEvent;
use Webong\Gateway\Protocol\Protocol;

final class FakeEmailTxtResolver implements TxtResolver
{
    /** @var list<string> */
    public array $values = [];

    public bool $unavailable = false;

    /** @var array<string, list<string>> */
    public array $dmarcRecords = [];

    public function records(string $name): array
    {
        if ($this->unavailable) {
            throw new RuntimeException('Resolver unavailable.');
        }

        return str_starts_with($name, '_gateway-verify.') ? $this->values : ($this->dmarcRecords[$name] ?? []);
    }
}

final class FakeEmailMessagePolicy implements EmailMessagePolicy
{
    public EmailPolicyVerdict $verdict = EmailPolicyVerdict::Accept;

    /** @var list<string> */
    public array $recipients = [];

    public string $payload = '';

    public function assess(GatewayEvent $event, array $recipients, ?DmarcAssessment $authentication): EmailPolicyVerdict
    {
        $this->recipients = $recipients;
        $this->payload = $event->payload;

        return $this->verdict;
    }
}

final class FakeEmailSubmissionAuthenticator implements EmailSubmissionAuthenticator
{
    public function authorize(GatewayEvent $event): ?EmailSubmissionGrant
    {
        return ($event->attributes['password'] ?? '') === 'secret'
            ? new EmailSubmissionGrant('account-123', ['sender@example.test'], dailyLimit: 2, dailyByteLimit: 64) : null;
    }
}

final class FakeMailboxAuthenticator implements MailboxAuthenticator
{
    public function authorize(GatewayEvent $event): ?MailboxAccessGrant
    {
        return ($event->attributes['password'] ?? '') === 'secret'
            ? new MailboxAccessGrant('account-123', 'primary', 'upstream-alice', 'upstream-secret') : null;
    }
}

it('wires a deployment-owned mailbox authenticator for POP3 and IMAP', function (): void {
    config()->set('gateway.email.mailbox_authenticator', FakeMailboxAuthenticator::class);
    $planner = app(ProtocolPlanner::class);
    foreach ([Protocol::POP3, Protocol::IMAP] as $protocol) {
        $accepted = $planner->planEvent(new GatewayEvent(
            id: $protocol->value.'-auth-accepted', protocol: $protocol, kind: EventKind::AUTHENTICATE,
            route: 'alice@example.test', attributes: ['username' => 'alice@example.test', 'password' => 'secret'],
        ));
        $rejected = $planner->planEvent(new GatewayEvent(
            id: $protocol->value.'-auth-rejected', protocol: $protocol, kind: EventKind::AUTHENTICATE,
            route: 'alice@example.test', attributes: ['username' => 'alice@example.test', 'password' => 'wrong'],
        ));

        expect($accepted->action)->toBe(GatewayAction::ACCEPT)
            ->and($accepted->metadata['upstream_id'])->toBe('primary')
            ->and($rejected->action)->toBe(GatewayAction::REJECT);
    }
});

it('wires a deployment-owned SMTP authenticator without exposing credentials to registry routes', function (): void {
    config()->set('gateway.email.submission_authenticator', FakeEmailSubmissionAuthenticator::class);
    $planner = app(ProtocolPlanner::class);
    $accepted = $planner->planEvent(new GatewayEvent(
        id: 'smtp-auth-accepted',
        protocol: Protocol::SMTP,
        kind: EventKind::AUTHENTICATE,
        route: 'subscriber',
        attributes: ['username' => 'subscriber', 'password' => 'secret'],
    ));
    $rejected = $planner->planEvent(new GatewayEvent(
        id: 'smtp-auth-rejected',
        protocol: Protocol::SMTP,
        kind: EventKind::AUTHENTICATE,
        route: 'subscriber',
        attributes: ['username' => 'subscriber', 'password' => 'wrong'],
    ));

    expect($accepted->action)->toBe(GatewayAction::ACCEPT)
        ->and($accepted->metadata['auth_identity'])->toBe('account-123')
        ->and($accepted->metadata['authorized_mail_from'])->toBe('["sender@example.test"]')
        ->and($accepted->metadata['submission_daily_limit'])->toBe('2')
        ->and($accepted->metadata['submission_daily_byte_limit'])->toBe('64')
        ->and($rejected->action)->toBe(GatewayAction::REJECT);
});

it('atomically limits authenticated SMTP attempts per canonical identity and UTC day', function (): void {
    $quota = app(EmailSubmissionQuota::class);
    expect($quota->consume('account-123', 2))->toBeTrue()
        ->and($quota->consume('account-123', 2))->toBeTrue()
        ->and($quota->consume('account-123', 2))->toBeFalse()
        ->and($quota->consume('account-456', 2))->toBeTrue();

    $usage = DB::table('email_submission_usage')->where('identity_hash', hash('sha256', 'account-123'))->first();
    expect($usage->attempts)->toBe(2)
        ->and(DB::table('email_submission_usage')->where('identity_hash', 'account-123')->exists())->toBeFalse();
});

it('atomically limits authenticated SMTP bytes per canonical identity and UTC day', function (): void {
    $quota = app(EmailSubmissionQuota::class);
    expect($quota->consume('account-bytes', 0, 6, 10))->toBeTrue()
        ->and($quota->consume('account-bytes', 0, 5, 10))->toBeFalse()
        ->and($quota->consume('account-bytes', 0, 4, 10))->toBeTrue()
        ->and($quota->consume('account-bytes', 0, 1, 10))->toBeFalse();

    $usage = DB::table('email_submission_usage')->where('identity_hash', hash('sha256', 'account-bytes'))->first();
    expect($usage->attempts)->toBe(2)->and($usage->bytes)->toBe(10);

    expect($quota->consume('account-combined', 2, 6, 10))->toBeTrue()
        ->and($quota->consume('account-combined', 2, 5, 10))->toBeFalse()
        ->and($quota->consume('account-combined', 2, 4, 10))->toBeTrue();
    $combined = DB::table('email_submission_usage')->where('identity_hash', hash('sha256', 'account-combined'))->first();
    expect($combined->attempts)->toBe(2)->and($combined->bytes)->toBe(10);
});

it('resets submission quotas at the UTC day boundary and prunes old counters', function (): void {
    $quota = app(EmailSubmissionQuota::class);
    try {
        Carbon::setTestNow(Carbon::parse('2026-09-28 23:59:59', 'UTC'));
        expect($quota->consume('account-utc', 1))->toBeTrue()
            ->and($quota->consume('account-utc', 1))->toBeFalse();
        Carbon::setTestNow(Carbon::parse('2026-09-29 00:00:01', 'UTC'));
        expect($quota->consume('account-utc', 1))->toBeTrue()
            ->and(DB::table('email_submission_usage')->where('identity_hash', hash('sha256', 'account-utc'))->count())->toBe(2);
        Carbon::setTestNow(Carbon::parse('2026-10-01 00:00:01', 'UTC'));
        expect($quota->prune(1))->toBe(2)
            ->and(DB::table('email_submission_usage')->where('identity_hash', hash('sha256', 'account-utc'))->count())->toBe(0);
    } finally {
        Carbon::setTestNow();
    }
});

it('registers the Email submission usage retention command', function (): void {
    DB::table('email_submission_usage')->insert([
        'identity_hash' => hash('sha256', 'old-account'),
        'day' => '2000-01-01',
        'attempts' => 1,
        'created_at' => now('UTC'),
        'updated_at' => now('UTC'),
    ]);
    DB::table('email_domain_submission_usage')->insert([
        'domain_hash' => hash('sha256', 'example.test'),
        'day' => '2000-01-01',
        'attempts' => 1,
        'bytes' => 5,
        'created_at' => now('UTC'),
        'updated_at' => now('UTC'),
    ]);
    config()->set('gateway.email.submission_usage_retention_days', 90);

    $this->artisan('gateway:prune-email-submission-usage')->assertExitCode(0);
    expect(DB::table('email_submission_usage')->where('identity_hash', hash('sha256', 'old-account'))->exists())->toBeFalse()
        ->and(DB::table('email_domain_submission_usage')->where('domain_hash', hash('sha256', 'example.test'))->exists())->toBeFalse();
});

it('shares sender-domain limits across accounts and rolls back a rejected account charge', function (): void {
    config()->set('gateway.email.submission_domain_daily_limit', 1);
    config()->set('gateway.email.submission_domain_daily_byte_limit', 12);
    $quota = app(EmailSubmissionQuota::class);

    expect($quota->consumeSubmission('account-a', 'first@EXAMPLE.TEST.', 1, 6, 10))->toBeTrue()
        ->and($quota->consumeSubmission('account-b', 'second@example.test', 1, 6, 10))->toBeFalse()
        ->and($quota->consumeSubmission('account-b', 'second@other.test', 1, 6, 10))->toBeTrue()
        ->and(DB::table('email_submission_usage')->where('identity_hash', hash('sha256', 'account-b'))->value('attempts'))->toBe(1)
        ->and(DB::table('email_domain_submission_usage')->where('domain_hash', hash('sha256', 'example.test'))->value('attempts'))->toBe(1);
});

it('enforces sender-domain byte limits even when account quotas are disabled', function (): void {
    config()->set('gateway.email.submission_domain_daily_byte_limit', 10);
    $quota = app(EmailSubmissionQuota::class);

    expect($quota->consumeSubmission('account-a', 'first@example.test', 0, 6, 0))->toBeTrue()
        ->and($quota->consumeSubmission('account-b', 'second@example.test', 0, 5, 0))->toBeFalse()
        ->and($quota->consumeSubmission('account-b', 'second@example.test', 0, 4, 0))->toBeTrue()
        ->and(DB::table('email_domain_submission_usage')->where('domain_hash', hash('sha256', 'example.test'))->value('bytes'))->toBe(10)
        ->and(DB::table('email_submission_usage')->count())->toBe(0);
});

it('enforces a granted account quota before routing an SMTP transaction', function (): void {
    config()->set('gateway.email.submission_daily_limit', 1);
    $planner = app(ProtocolPlanner::class);
    $event = static fn (string $id): GatewayEvent => new GatewayEvent(
        id: $id,
        protocol: Protocol::SMTP,
        kind: EventKind::TRANSACTION,
        route: 'recipient@example.test',
        attributes: [
            'auth_username' => 'SUBSCRIBER',
            'auth_identity' => 'account-123',
            'submission_daily_limit' => '2',
            'mail_from' => 'sender@example.test',
            'recipients' => 'recipient@example.test',
        ],
        payload: "From: sender@example.test\r\nSubject: test\r\n\r\nbody\r\n",
    );
    $first = $planner->planEvent($event('submission-1'));
    $second = $planner->planEvent($event('submission-2'));
    $third = $planner->planEvent($event('submission-3'));

    expect($first->statusCode)->not->toBe(452)
        ->and($second->statusCode)->not->toBe(452)
        ->and($third->statusCode)->toBe(452)
        ->and(DB::table('email_submission_usage')->where('identity_hash', hash('sha256', 'account-123'))->value('attempts'))->toBe(2);
});

it('enforces the shared sender-domain cap before routing authenticated SMTP transactions', function (): void {
    config()->set('gateway.email.submission_daily_limit', 1);
    config()->set('gateway.email.submission_domain_daily_limit', 1);
    $planner = app(ProtocolPlanner::class);
    $event = static fn (string $id, string $identity): GatewayEvent => new GatewayEvent(
        id: $id, protocol: Protocol::SMTP, kind: EventKind::TRANSACTION,
        route: 'recipient@example.test',
        attributes: [
            'auth_username' => $identity,
            'auth_identity' => $identity,
            'mail_from' => 'sender@example.test',
            'recipients' => 'recipient@example.test',
        ],
        payload: "From: sender@example.test\r\nSubject: test\r\n\r\nbody\r\n",
    );

    expect($planner->planEvent($event('domain-1', 'account-a'))->statusCode)->not->toBe(452)
        ->and($planner->planEvent($event('domain-2', 'account-b'))->statusCode)->toBe(452)
        ->and(DB::table('email_submission_usage')->where('identity_hash', hash('sha256', 'account-b'))->exists())->toBeFalse()
        ->and(DB::table('email_domain_submission_usage')->where('domain_hash', hash('sha256', 'example.test'))->value('attempts'))->toBe(1);
});

it('enforces the configured submission byte cap before routing SMTP DATA', function (): void {
    config()->set('gateway.email.submission_daily_limit', 0);
    config()->set('gateway.email.submission_daily_byte_limit', 50);
    $planner = app(ProtocolPlanner::class);
    $event = static fn (string $id, int $bytes): GatewayEvent => new GatewayEvent(
        id: $id, protocol: Protocol::SMTP, kind: EventKind::TRANSACTION,
        route: 'recipient@example.test',
        attributes: ['auth_username' => 'subscriber', 'auth_identity' => 'account-123', 'mail_from' => 'sender@example.test'],
        payload: str_repeat('x', $bytes),
    );

    expect($planner->planEvent($event('bytes-1', 30))->statusCode)->not->toBe(452)
        ->and($planner->planEvent($event('bytes-2', 25))->statusCode)->toBe(452)
        ->and($planner->planEvent($event('bytes-3', 20))->statusCode)->not->toBe(452)
        ->and(DB::table('email_submission_usage')->where('identity_hash', hash('sha256', 'account-123'))->value('bytes'))->toBe(50);
});

it('does not apply the submission byte cap to unauthenticated inbound mail', function (): void {
    config()->set('gateway.email.submission_daily_byte_limit', 1);
    $decision = app(ProtocolPlanner::class)->planEvent(new GatewayEvent(
        id: 'inbound-not-submission', protocol: Protocol::SMTP, kind: EventKind::TRANSACTION,
        route: 'recipient@example.test',
        attributes: ['mail_from' => 'sender@other.test'],
        payload: str_repeat('x', 100),
    ));

    expect($decision->statusCode)->not->toBe(452)
        ->and(DB::table('email_submission_usage')->count())->toBe(0);
});

it('requires registry authentication and TXT ownership before enabling a domain', function (): void {
    $this->postJson('/email/domains', ['domain' => 'example.test'])->assertUnauthorized();
    $this->withToken('registry-secret')->postJson('/email/domains', ['domain' => 'localhost'])->assertUnprocessable();

    $domain = $this->withToken('registry-secret')->postJson('/email/domains', [
        'domain' => 'Example.Test.',
    ])->assertCreated()->json();

    expect($domain['domain'])->toBe('example.test')
        ->and($domain['enabled'])->toBeFalse()
        ->and($domain['verified_at'])->toBeNull()
        ->and($domain['verification']['name'])->toBe('_gateway-verify.example.test');

    $resolver = new FakeEmailTxtResolver();
    $this->app->instance(TxtResolver::class, $resolver);
    $this->withToken('registry-secret')->patchJson('/email/domains/'.$domain['id'], ['enabled' => true])->assertConflict();
    $this->withToken('registry-secret')->postJson('/email/domains/'.$domain['id'].'/verify')->assertConflict();

    $resolver->values = [$domain['verification']['value']];
    $this->app->instance(TxtResolver::class, $resolver);
    $this->withToken('registry-secret')->postJson('/email/domains/'.$domain['id'].'/verify')->assertOk()
        ->assertJsonPath('enabled', false);
    $this->withToken('registry-secret')->patchJson('/email/domains/'.$domain['id'], ['enabled' => true])->assertOk()
        ->assertJsonPath('enabled', true);
    $this->withToken('registry-secret')->postJson('/email/domains', ['domain' => 'example.test'])->assertConflict();
});

it('rechecks TXT ownership before re-enabling a managed email domain', function (): void {
    $resolver = new FakeEmailTxtResolver();
    $this->app->instance(TxtResolver::class, $resolver);
    $domain = $this->withToken('registry-secret')->postJson('/email/domains', ['domain' => 'owned.test'])->assertCreated()->json();
    $resolver->values = [$domain['verification']['value']];
    $this->withToken('registry-secret')->postJson('/email/domains/'.$domain['id'].'/verify')->assertOk();
    $this->withToken('registry-secret')->patchJson('/email/domains/'.$domain['id'], ['enabled' => true])->assertOk();
    $this->withToken('registry-secret')->patchJson('/email/domains/'.$domain['id'], ['enabled' => false])->assertOk();

    $resolver->values = [];
    $this->withToken('registry-secret')->postJson('/email/domains/'.$domain['id'].'/verify')->assertConflict();
    $this->withToken('registry-secret')->patchJson('/email/domains/'.$domain['id'], ['enabled' => true])->assertConflict();
    $this->withToken('registry-secret')->getJson('/email/domains/'.$domain['id'])->assertJsonPath('enabled', false);

    $resolver->unavailable = true;
    $this->withToken('registry-secret')->patchJson('/email/domains/'.$domain['id'], ['enabled' => true])->assertServiceUnavailable();
    $this->withToken('registry-secret')->getJson('/email/domains/'.$domain['id'])->assertJsonPath('enabled', false);

    $resolver->unavailable = false;
    $resolver->values = [$domain['verification']['value']];
    $this->withToken('registry-secret')->patchJson('/email/domains/'.$domain['id'], ['enabled' => true])->assertOk()
        ->assertJsonPath('enabled', true);
});

it('temporarily fails SMTP planning when an enabled domain needs TXT revalidation', function (): void {
    config()->set('gateway.email.verification_ttl_seconds', 3600);
    $resolver = new FakeEmailTxtResolver();
    $this->app->instance(TxtResolver::class, $resolver);
    $domain = $this->withToken('registry-secret')->postJson('/email/domains', ['domain' => 'freshness.test'])->assertCreated()->json();
    $resolver->values = [$domain['verification']['value']];
    $this->withToken('registry-secret')->patchJson('/email/domains/'.$domain['id'], ['enabled' => true])->assertOk();
    $this->withToken('registry-secret')->postJson('/email/domains/'.$domain['id'].'/aliases', [
        'local_part' => 'inbox', 'recipients' => ['target@example.net'],
    ])->assertCreated();

    $event = new GatewayEvent(
        id: 'smtp-freshness', protocol: Protocol::SMTP, kind: EventKind::TRANSACTION,
        route: 'inbox@freshness.test', attributes: ['recipients' => 'inbox@freshness.test'],
        payload: "Subject: freshness\r\n\r\nbody\r\n",
    );
    $planner = app(ProtocolPlanner::class);
    $resolver->values = [];
    expect($planner->planEvent($event)->action)->toBe(GatewayAction::SUBMIT);

    $stale = now()->subHours(2);
    DB::table('email_domains')->where('id', $domain['id'])->update(['verified_at' => $stale]);
    expect(fn (): mixed => $planner->planEvent($event))->toThrow(VerificationRecordMissing::class);
    $recipient = new GatewayEvent(
        id: 'smtp-freshness-recipient', protocol: Protocol::SMTP, kind: EventKind::RECIPIENT,
        route: 'inbox@freshness.test',
    );
    expect(fn (): mixed => $planner->planEvent($recipient))->toThrow(VerificationRecordMissing::class);
    $resolver->values = [$domain['verification']['value']];
    expect($planner->planEvent($event)->action)->toBe(GatewayAction::SUBMIT);
    expect(strtotime((string) DB::table('email_domains')->where('id', $domain['id'])->value('verified_at')))
        ->toBeGreaterThan($stale->getTimestamp());

    config()->set('gateway.email.verification_ttl_seconds', 0);
    $resolver->values = [];
    expect(fn (): mixed => $planner->planEvent($event))->toThrow(VerificationRecordMissing::class);
});

it('manages exact aliases within their domain and rejects malformed targets', function (): void {
    $first = $this->withToken('registry-secret')->postJson('/email/domains', ['domain' => 'first.test'])->assertCreated()->json();
    $second = $this->withToken('registry-secret')->postJson('/email/domains', ['domain' => 'second.test'])->assertCreated()->json();

    $base = '/email/domains/'.$first['id'].'/aliases';
    $this->withToken('registry-secret')->postJson($base, [
        'local_part' => 'contact', 'recipients' => ['not-an-email'],
    ])->assertUnprocessable();
    $alias = $this->withToken('registry-secret')->postJson($base, [
        'local_part' => 'Contact', 'recipients' => ['one@example.net', 'two@example.net'],
    ])->assertCreated()->json();

    expect($alias['local_part'])->toBe('contact')
        ->and($alias['enabled'])->toBeTrue()
        ->and($alias['recipients'])->toBe(['one@example.net', 'two@example.net']);

    $this->withToken('registry-secret')->postJson($base, [
        'local_part' => 'contact', 'recipients' => ['one@example.net'],
    ])->assertConflict();
    $this->withToken('registry-secret')->getJson('/email/domains/'.$second['id'].'/aliases/'.$alias['id'])->assertNotFound();
    $this->withToken('registry-secret')->patchJson($base.'/'.$alias['id'], [
        'recipients' => ['three@example.net'], 'enabled' => false,
    ])->assertOk()->assertJsonPath('recipients.0', 'three@example.net')->assertJsonPath('enabled', false);
    $this->withToken('registry-secret')->deleteJson($base.'/'.$alias['id'])->assertNoContent();
    $this->withToken('registry-secret')->getJson($base.'/'.$alias['id'])->assertNotFound();
    $this->withToken('registry-secret')->deleteJson('/email/domains/'.$first['id'])->assertNoContent();
});

it('routes exact, longest-prefix pattern, and catch-all aliases without bypassing paused matches', function (): void {
    $resolver = new FakeEmailTxtResolver();
    $this->app->instance(TxtResolver::class, $resolver);
    $domain = $this->withToken('registry-secret')->postJson('/email/domains', ['domain' => 'catchall.test'])->assertCreated()->json();
    $resolver->values = [$domain['verification']['value']];
    $this->withToken('registry-secret')->patchJson('/email/domains/'.$domain['id'], ['enabled' => true])->assertOk();
    $base = '/email/domains/'.$domain['id'].'/aliases';
    $this->withToken('registry-secret')->postJson($base, [
        'local_part' => 'prefix*', 'recipients' => ['invalid@example.net'],
    ])->assertCreated();
    $this->withToken('registry-secret')->postJson($base, [
        'local_part' => 'pre*fix', 'recipients' => ['invalid@example.net'],
    ])->assertUnprocessable();
    $catchAll = $this->withToken('registry-secret')->postJson($base, [
        'local_part' => '*', 'recipients' => ['fallback@example.net'],
    ])->assertCreated()->json();
    $this->withToken('registry-secret')->postJson($base, [
        'local_part' => '*', 'recipients' => ['duplicate@example.net'],
    ])->assertConflict();
    $exact = $this->withToken('registry-secret')->postJson($base, [
        'local_part' => 'known', 'recipients' => ['exact@example.net'],
    ])->assertCreated()->json();
    $shortPattern = $this->withToken('registry-secret')->postJson($base, [
        'local_part' => 'team*', 'recipients' => ['team@example.net'],
    ])->assertCreated()->json();
    $longPattern = $this->withToken('registry-secret')->postJson($base, [
        'local_part' => 'team-pay*', 'recipients' => ['payments@example.net'],
    ])->assertCreated()->json();
    $exactPatternOverlap = $this->withToken('registry-secret')->postJson($base, [
        'local_part' => 'team-payroll', 'recipients' => ['payroll@example.net'],
    ])->assertCreated()->json();

    $planner = app(ProtocolPlanner::class);
    $transaction = static fn (string $address): GatewayEvent => new GatewayEvent(
        id: 'smtp-'.$address, protocol: Protocol::SMTP, kind: EventKind::TRANSACTION,
        route: $address, attributes: ['recipients' => $address], payload: "Subject: catch-all\r\n\r\nbody\r\n",
    );
    $targets = static fn (GatewayEvent $event): array => json_decode(
        $planner->planEvent($event)->deliveries[0]->attributes['recipients'], true, 512, JSON_THROW_ON_ERROR,
    );
    expect($targets($transaction('unknown@catchall.test')))->toBe(['fallback@example.net'])
        ->and($targets($transaction('known@catchall.test')))->toBe(['exact@example.net'])
        ->and($targets($transaction('team-news@catchall.test')))->toBe(['team@example.net'])
        ->and($targets($transaction('team-payments@catchall.test')))->toBe(['payments@example.net'])
        ->and($targets($transaction('team-payroll@catchall.test')))->toBe(['payroll@example.net']);

    $this->withToken('registry-secret')->patchJson($base.'/'.$exact['id'], ['enabled' => false])->assertOk();
    expect($planner->planEvent($transaction('known@catchall.test'))->action)->toBe(GatewayAction::REJECT);
    $this->withToken('registry-secret')->patchJson($base.'/'.$longPattern['id'], ['enabled' => false])->assertOk();
    expect($planner->planEvent($transaction('team-payments@catchall.test'))->action)->toBe(GatewayAction::REJECT);
    $this->withToken('registry-secret')->patchJson($base.'/'.$exactPatternOverlap['id'], ['enabled' => false])->assertOk();
    expect($planner->planEvent($transaction('team-payroll@catchall.test'))->action)->toBe(GatewayAction::REJECT);
    $this->withToken('registry-secret')->patchJson($base.'/'.$shortPattern['id'], ['enabled' => false])->assertOk();
    expect($planner->planEvent($transaction('team-news@catchall.test'))->action)->toBe(GatewayAction::REJECT);
    $this->withToken('registry-secret')->patchJson($base.'/'.$catchAll['id'], ['enabled' => false])->assertOk();
    expect($planner->planEvent($transaction('unknown@catchall.test'))->action)->toBe(GatewayAction::REJECT);
});

it('submits all verified alias recipients as one SMTP relay transaction', function (): void {
    $resolver = new FakeEmailTxtResolver();
    $this->app->instance(TxtResolver::class, $resolver);
    $domain = $this->withToken('registry-secret')->postJson('/email/domains', ['domain' => 'managed.test'])->assertCreated()->json();
    $resolver->values = [$domain['verification']['value']];
    $this->withToken('registry-secret')->postJson('/email/domains/'.$domain['id'].'/verify')->assertOk();
    $this->withToken('registry-secret')->patchJson('/email/domains/'.$domain['id'], ['enabled' => true])->assertOk();
    $base = '/email/domains/'.$domain['id'].'/aliases';
    $this->withToken('registry-secret')->postJson($base, [
        'local_part' => 'first', 'recipients' => ['one@example.net', 'shared@example.net'],
    ])->assertCreated();
    $this->withToken('registry-secret')->postJson($base, [
        'local_part' => 'second', 'recipients' => ['two@example.net', 'shared@example.net'],
    ])->assertCreated();
    $this->withToken('registry-secret')->postJson($base, [
        'local_part' => 'loop', 'recipients' => ['first@managed.test'],
    ])->assertUnprocessable();

    $planner = app(ProtocolPlanner::class);
    $event = new GatewayEvent(
        id: 'smtp-managed-1', protocol: Protocol::SMTP, kind: EventKind::TRANSACTION,
        route: 'first@managed.test',
        attributes: ['mail_from' => 'sender@example.org', 'recipients' => 'first@managed.test,second@managed.test'],
        payload: "Subject: test\r\n\r\nbody\r\n",
    );
    $decision = $planner->planEvent($event);
    expect($decision->action)->toBe(GatewayAction::SUBMIT)
        ->and($decision->deliveries)->toHaveCount(1)
        ->and($decision->deliveries[0]->protocol)->toBe(Protocol::SMTP)
        ->and($decision->deliveries[0]->payload)->toBe($event->payload)
        ->and(json_decode($decision->deliveries[0]->attributes['recipients'], true))->toBe([
            'one@example.net', 'shared@example.net', 'two@example.net',
        ]);

    $missing = new GatewayEvent(
        id: 'smtp-managed-2', protocol: Protocol::SMTP, kind: EventKind::TRANSACTION,
        route: 'first@managed.test',
        attributes: ['recipients' => 'first@managed.test,unknown@managed.test'],
    );
    expect($planner->planEvent($missing)->action)->toBe(GatewayAction::REJECT);
    $mixed = new GatewayEvent(
        id: 'smtp-managed-3', protocol: Protocol::SMTP, kind: EventKind::TRANSACTION,
        route: 'first@managed.test',
        attributes: ['recipients' => 'first@managed.test,other@unmanaged.test'],
    );
    expect($planner->planEvent($mixed)->action)->toBe(GatewayAction::REJECT);

    $recipient = new GatewayEvent(
        id: 'smtp-recipient-1', protocol: Protocol::SMTP, kind: EventKind::RECIPIENT,
        route: 'first@managed.test', attributes: ['mail_from' => 'sender@example.org'],
    );
    expect($planner->planEvent($recipient)->action)->toBe(GatewayAction::ACCEPT);
    $unknownRecipient = new GatewayEvent(
        id: 'smtp-recipient-2', protocol: Protocol::SMTP, kind: EventKind::RECIPIENT,
        route: 'unknown@managed.test',
    );
    expect($planner->planEvent($unknownRecipient)->action)->toBe(GatewayAction::REJECT)
        ->and($planner->planEvent($unknownRecipient)->statusCode)->toBe(550);
    $mixedRecipient = new GatewayEvent(
        id: 'smtp-recipient-3', protocol: Protocol::SMTP, kind: EventKind::RECIPIENT,
        route: 'other@unmanaged.test', attributes: ['accepted_recipients' => '["first@managed.test"]'],
    );
    expect($planner->planEvent($mixedRecipient)->action)->toBe(GatewayAction::REJECT);
    $unmanagedRecipient = new GatewayEvent(
        id: 'smtp-recipient-4', protocol: Protocol::SMTP, kind: EventKind::RECIPIENT,
        route: 'other@unmanaged.test',
    );
    expect($planner->planEvent($unmanagedRecipient)->action)->toBe(GatewayAction::ACCEPT);
});

it('lets a trusted Email policy reject or defer a complete message before relay submission', function (): void {
    $resolver = new FakeEmailTxtResolver();
    $this->app->instance(TxtResolver::class, $resolver);
    $domain = $this->withToken('registry-secret')->postJson('/email/domains', ['domain' => 'policy.test'])->assertCreated()->json();
    $resolver->values = [$domain['verification']['value']];
    $this->withToken('registry-secret')->patchJson('/email/domains/'.$domain['id'], ['enabled' => true])->assertOk();
    $this->withToken('registry-secret')->postJson('/email/domains/'.$domain['id'].'/aliases', [
        'local_part' => 'inbox', 'recipients' => ['target@example.net'],
    ])->assertCreated();

    $policy = new FakeEmailMessagePolicy();
    $this->app->instance(FakeEmailMessagePolicy::class, $policy);
    config()->set('gateway.email.policy', FakeEmailMessagePolicy::class);
    $planner = app(ProtocolPlanner::class);
    $event = new GatewayEvent(
        id: 'smtp-policy', protocol: Protocol::SMTP, kind: EventKind::TRANSACTION,
        route: 'inbox@policy.test', attributes: ['mail_from' => 'sender@example.org', 'recipients' => 'inbox@policy.test'],
        payload: "Subject: policy\r\n\r\nmessage\r\n",
    );

    $policy->verdict = EmailPolicyVerdict::Reject;
    $rejected = $planner->planEvent($event);
    expect($rejected->action)->toBe(GatewayAction::REJECT)
        ->and($rejected->statusCode)->toBe(550)
        ->and($rejected->deliveries)->toBeEmpty()
        ->and($policy->recipients)->toBe(['target@example.net'])
        ->and($policy->payload)->toBe($event->payload);

    $policy->verdict = EmailPolicyVerdict::Defer;
    expect($planner->planEvent($event)->statusCode)->toBe(451);

    $policy->verdict = EmailPolicyVerdict::Accept;
    expect($planner->planEvent($event)->action)->toBe(GatewayAction::SUBMIT);

    config()->set('gateway.email.policy', stdClass::class);
    expect(fn (): ProtocolPlanner => app(ProtocolPlanner::class))->toThrow(RuntimeException::class, 'must implement EmailMessagePolicy');
});

it('assesses DMARC before managed forwarding and quarantines opted-in failures', function (): void {
    $resolver = new FakeEmailTxtResolver();
    $this->app->instance(TxtResolver::class, $resolver);
    $domain = $this->withToken('registry-secret')->postJson('/email/domains', ['domain' => 'managed.test'])->assertCreated()->json();
    $resolver->values = [$domain['verification']['value']];
    $this->withToken('registry-secret')->patchJson('/email/domains/'.$domain['id'], ['enabled' => true])->assertOk();
    $this->withToken('registry-secret')->postJson('/email/domains/'.$domain['id'].'/aliases', [
        'local_part' => 'inbox', 'recipients' => ['target@example.net'],
    ])->assertCreated();
    $resolver->dmarcRecords['_dmarc.sender.example'] = ['v=DMARC1; p=reject'];

    $planner = app(ProtocolPlanner::class);
    $attributes = [
        'mail_from' => 'alice@sender.example',
        'recipients' => 'inbox@managed.test',
        'rfc5322_from_domain' => 'sender.example',
        'rfc5322_from_organizational_domain' => 'sender.example',
        'spf_result' => 'fail',
        'dkim_result' => 'none',
    ];
    $event = static fn (array $values): GatewayEvent => new GatewayEvent(
        id: 'smtp-managed-dmarc', protocol: Protocol::SMTP, kind: EventKind::TRANSACTION,
        route: 'inbox@managed.test', attributes: $values, payload: "From: Alice <alice@sender.example>\r\n\r\nbody\r\n",
    );

    $monitored = $planner->planEvent($event($attributes));
    expect($monitored->action)->toBe(GatewayAction::SUBMIT)
        ->and($monitored->metadata['dmarc_result'])->toBe('fail')
        ->and($monitored->metadata['dmarc_policy'])->toBe('reject');

    config()->set('gateway.email.dmarc_enforce_enabled', true);
    $quarantined = $planner->planEvent($event($attributes));
    expect($quarantined->action)->toBe(GatewayAction::QUARANTINE)
        ->and($quarantined->statusCode)->toBe(250)
        ->and($quarantined->deliveries)->toBe([]);

    $resolver->dmarcRecords['_dmarc.sender.example'] = ['v=DMARC1; p=quarantine'];
    expect($planner->planEvent($event($attributes))->action)->toBe(GatewayAction::QUARANTINE);

    $resolver->dmarcRecords['_dmarc.sender.example'] = ['v=DMARC1; p=quarantine; t=y'];
    expect($planner->planEvent($event($attributes))->action)->toBe(GatewayAction::SUBMIT);

    $resolver->dmarcRecords['_dmarc.sender.example'] = ['v=DMARC1; p=reject; t=y'];
    expect($planner->planEvent($event($attributes))->action)->toBe(GatewayAction::QUARANTINE);

    $aligned = $planner->planEvent($event([...$attributes,
        'dkim_result' => 'pass',
        'dkim_signatures' => '[{"domain":"sender.example","organizational_domain":"sender.example","result":"pass"}]',
    ]));
    expect($aligned->action)->toBe(GatewayAction::SUBMIT)
        ->and($aligned->metadata['dmarc_result'])->toBe('pass');
});
