<?php

declare(strict_types=1);

use Illuminate\Http\Client\Request as ClientRequest;
use Illuminate\Http\Client\ConnectionException;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Http;
use Illuminate\Support\Carbon;
use Webong\Gateway\Email\EmailSubmissionAuthenticator;
use Webong\Gateway\Email\EmailSubmissionGrant;
use Webong\Gateway\Protocols\GatewayEvent;

final class HttpEmailSubmissionAuthenticator implements EmailSubmissionAuthenticator
{
    public function authorize(GatewayEvent $event): ?EmailSubmissionGrant
    {
        if (($event->attributes['password'] ?? '') !== 'secret') {
            return null;
        }

        return match ($event->attributes['username'] ?? '') {
            'alice' => new EmailSubmissionGrant('account-alice', ['alice@example.test'], dailyLimit: 1),
            'bob' => new EmailSubmissionGrant('account-bob', ['bob@example.test'], dailyLimit: 1),
            default => null,
        };
    }
}

beforeEach(function (): void {
    config()->set('gateway.email.submission_authenticator', HttpEmailSubmissionAuthenticator::class);
    app(\Webong\Gateway\Email\EmailRuntimePolicyStore::class)->create(['http_submission_enabled' => true]);
    config()->set('gateway.delivery_admin_url', 'http://spinner.internal:5052');
});

it('keeps HTTP submission off unless explicitly enabled', function (): void {
    $policy = app(\Webong\Gateway\Email\EmailRuntimePolicyStore::class);
    $policy->replace(['http_submission_enabled' => false], $policy->find()->revision);
    Http::fake();
    $this->withHeaders(['Authorization' => 'Basic '.base64_encode('alice:secret'), 'Idempotency-Key' => 'email-1'])
        ->postJson('https://localhost/email/submissions', emailSubmissionBody())->assertNotFound();
    Http::assertNothingSent();
});

function emailSubmissionBody(string $sender = 'alice@example.test'): array
{
    return [
        'mail_from' => $sender,
        'recipients' => ['bob@example.test'],
        'message_base64' => base64_encode("From: alice@example.test\r\nSubject: hi\r\n\r\nhello"),
    ];
}

it('returns provider-neutral submission status only to the owning credential', function (): void {
    $state = 'queued';
    $assertion = null;
    Http::fake(function (ClientRequest $request) use (&$state, &$assertion) {
        if ($request->method() === 'POST') { return Http::response(['id' => $request['receipt_id']], 202); }
        $id = basename(parse_url($request->url(), PHP_URL_PATH));
        return Http::response(['id' => $id, 'state' => $state, 'attempts' => 1,
            'body_assertion' => $assertion], 200);
    });
    $headers = ['Authorization' => 'Basic '.base64_encode('alice:secret'), 'Idempotency-Key' => 'status-1'];
    $accepted = $this->withHeaders($headers)->postJson('https://localhost/email/submissions', emailSubmissionBody())
        ->assertAccepted()->json();
    $url = 'https://localhost'.$accepted['status_url'];
    $this->withHeaders(['Authorization' => 'Basic '.base64_encode('bob:secret')])->getJson($url)->assertNotFound();
    $this->withHeaders(['Authorization' => 'Basic '.base64_encode('alice:secret')])->getJson($url)
        ->assertOk()->assertJsonPath('state', 'queued');
    $state = 'delivered';
    $this->withHeaders(['Authorization' => 'Basic '.base64_encode('alice:secret')])->getJson($url)
        ->assertOk()->assertJsonPath('state', 'accepted');
    $state = 'failed'; $assertion = 'smtp_acceptance_unknown';
    $this->withHeaders(['Authorization' => 'Basic '.base64_encode('alice:secret')])->getJson($url)
        ->assertOk()->assertJsonPath('state', 'ambiguous')->assertJsonMissingPath('body_assertion');
});

it('blocks new HTTP submissions to an operator-suppressed recipient without charging quota', function (): void {
    Http::fake(fn (ClientRequest $request) => Http::response(['id' => $request['receipt_id']], 202));
    $this->postJson('/email/suppressions', ['address' => 'bob@example.test', 'reason' => 'hard_bounce'])->assertUnauthorized();
    $suppression = $this->withToken('registry-secret')->postJson('/email/suppressions', [
        'address' => 'bob@EXAMPLE.TEST', 'reason' => 'hard_bounce',
    ])->assertCreated()->json();
    expect($suppression['address'])->toBe('bob@example.test');
    $this->withToken('registry-secret')->getJson('/email/suppressions')->assertOk()->assertJsonCount(1, 'data');

    $headers = ['Authorization' => 'Basic '.base64_encode('alice:secret'), 'Idempotency-Key' => 'suppressed-new'];
    $this->withHeaders($headers)->postJson('https://localhost/email/submissions', emailSubmissionBody())->assertUnprocessable();
    expect(DB::table('usage_counters')->where('metric', 'email.submission.account')->count())->toBe(0)
        ->and(DB::table('email_http_submissions')->count())->toBe(0);
    Http::assertNothingSent();

    $this->withToken('registry-secret')->deleteJson('/email/suppressions/'.$suppression['id'])->assertNoContent();
    $accepted = $this->withHeaders($headers)->postJson('https://localhost/email/submissions', emailSubmissionBody())->assertAccepted()->json('id');
    $this->withToken('registry-secret')->postJson('/email/suppressions', [
        'address' => 'bob@example.test', 'reason' => 'manual',
    ])->assertCreated();
    $this->withHeaders($headers)->postJson('https://localhost/email/submissions', emailSubmissionBody())->assertAccepted()->assertJsonPath('id', $accepted);
    Http::assertSentCount(1);
});

it('lists suppression entries with a bounded cursor and rejects invalid mutations', function (): void {
    $this->withToken('registry-secret')->postJson('/email/suppressions', [
        'address' => 'one@example.test', 'reason' => 'manual',
    ])->assertCreated();
    $this->withToken('registry-secret')->postJson('/email/suppressions', [
        'address' => 'two@example.test', 'reason' => 'complaint',
    ])->assertCreated();
    $this->withToken('registry-secret')->postJson('/email/suppressions', [
        'address' => 'bad-address', 'reason' => 'manual',
    ])->assertUnprocessable();
    $this->withToken('registry-secret')->postJson('/email/suppressions', [
        'address' => 'three@example.test', 'reason' => 'unknown',
    ])->assertUnprocessable();
    $first = $this->withToken('registry-secret')->getJson('/email/suppressions?limit=1')
        ->assertOk()->assertJsonCount(1, 'data')->json();
    $second = $this->withToken('registry-secret')->getJson('/email/suppressions?limit=1&cursor='.$first['next_cursor'])
        ->assertOk()->assertJsonCount(1, 'data')->json();
    expect($second['data'][0]['id'])->not->toBe($first['data'][0]['id']);
    $this->withToken('registry-secret')->getJson('/email/suppressions?limit=101')->assertUnprocessable();
    $this->withToken('registry-secret')->deleteJson('/email/suppressions/'.str_repeat('0', 64))->assertNotFound();
});

it('requires HTTPS and the deployment-owned email credential', function (): void {
    Http::fake();
    $this->withHeaders(['Authorization' => 'Basic '.base64_encode('alice:secret'), 'Idempotency-Key' => 'email-1'])
        ->postJson('/email/submissions', emailSubmissionBody())->assertForbidden();
    $this->withHeaders(['Authorization' => 'Basic '.base64_encode('alice:wrong'), 'Idempotency-Key' => 'email-1'])
        ->postJson('https://localhost/email/submissions', emailSubmissionBody())->assertUnauthorized();
    Http::assertNothingSent();
});

it('requires a bounded idempotency key before authentication', function (): void {
    Http::fake();
    $this->withHeaders(['Authorization' => 'Basic '.base64_encode('alice:secret')])
        ->postJson('https://localhost/email/submissions', emailSubmissionBody())->assertUnprocessable();
    $this->withHeaders(['Authorization' => 'Basic '.base64_encode('alice:secret'), 'Idempotency-Key' => str_repeat('a', 129)])
        ->postJson('https://localhost/email/submissions', emailSubmissionBody())->assertUnprocessable();
    Http::assertNothingSent();
});

it('authenticates, applies the shared quota, and hands message bytes to a private durable queue', function (): void {
    Http::fake(fn (ClientRequest $request) => Http::response(['id' => $request['receipt_id'], 'state' => 'queued'], 202));
    $response = $this->withHeaders(['Authorization' => 'Basic '.base64_encode('alice:secret'), 'Idempotency-Key' => 'email-1'])
        ->postJson('https://localhost/email/submissions', emailSubmissionBody());
    $response->assertAccepted();
    $id = $response->json('id');
    expect($id)->toMatch('/\A[a-f0-9]{32}\z/');
    $response->assertJsonPath('delivery_url', '/deliveries/'.$id);
    Http::assertSent(fn (ClientRequest $request): bool => $request->url() === 'http://spinner.internal:5052/email/submissions'
        && $request->hasHeader('Authorization', 'Bearer internal-secret')
        && $request->hasHeader('Content-Type', 'application/json')
        && $request['receipt_id'] === $id
        && $request['mail_from'] === 'alice@example.test'
        && $request['message'] === emailSubmissionBody()['message_base64']
        && ! str_contains($request->body(), 'secret'));
    expect(DB::table('usage_counters')->where('metric', 'email.submission.account')->where('subject_hash', hash('sha256', 'account-alice'))->value('attempts'))->toBe(1);

    $this->withHeaders(['Authorization' => 'Basic '.base64_encode('alice:secret'), 'Idempotency-Key' => 'email-1'])
        ->postJson('https://localhost/email/submissions', emailSubmissionBody())->assertAccepted()->assertJsonPath('id', $id);
    Http::assertSentCount(1);

    $this->withHeaders(['Authorization' => 'Basic '.base64_encode('alice:secret'), 'Idempotency-Key' => 'email-2'])
        ->postJson('https://localhost/email/submissions', emailSubmissionBody())->assertStatus(429);
    Http::assertSentCount(1);
});

it('rejects an ungranted envelope sender before the queue handoff', function (): void {
    Http::fake();
    $this->withHeaders(['Authorization' => 'Basic '.base64_encode('alice:secret'), 'Idempotency-Key' => 'email-1'])
        ->postJson('https://localhost/email/submissions', emailSubmissionBody('spoof@example.test'))->assertUnauthorized();
    Http::assertNothingSent();
});

it('pins an uncertain submission to the same receipt and node without charging quota twice', function (): void {
    $receipts = [];
    $urls = [];
    config()->set('gateway.delivery_admin_nodes', [
        'north' => 'http://north.internal:5052',
        'south' => 'http://south.internal:5052',
    ]);
    Http::fake(function (ClientRequest $request) use (&$receipts, &$urls) {
        $receipts[] = $request['receipt_id'];
        $urls[] = $request->url();
        if (count($receipts) === 1) {
            throw new ConnectionException('response lost after acceptance');
        }

        return Http::response(['id' => $request['receipt_id'], 'state' => 'queued'], 202);
    });
    $headers = ['Authorization' => 'Basic '.base64_encode('alice:secret'), 'Idempotency-Key' => 'uncertain-1'];
    $this->withHeaders($headers)->postJson('https://localhost/email/submissions', emailSubmissionBody())->assertServiceUnavailable();
    config()->set('gateway.delivery_admin_nodes', [
        'south' => 'http://south.internal:5052',
        'north' => 'http://north.internal:5052',
    ]);
    $this->withHeaders($headers)->postJson('https://localhost/email/submissions', emailSubmissionBody())->assertAccepted()
        ->assertJsonPath('id', $receipts[0]);
    expect($receipts)->toHaveCount(2)->and($receipts[0])->toBe($receipts[1])
        ->and($urls[0])->toBe($urls[1])
        ->and(DB::table('usage_counters')->where('metric', 'email.submission.account')->where('subject_hash', hash('sha256', 'account-alice'))->value('attempts'))->toBe(1);
});

it('replays an idempotent HTTP submission across shared-queue admin origins', function (): void {
    config()->set('gateway.delivery_admin_shared_urls', [
        'http://north.internal:5052', 'http://south.internal:5052',
    ]);
    $receipts = [];
    Http::fake(function (ClientRequest $request) use (&$receipts) {
        $receipts[] = $request['receipt_id'];
        if (str_contains($request->url(), 'north.internal')) {
            throw new ConnectionException('north response lost after acceptance');
        }

        return Http::response(['id' => $request['receipt_id'], 'state' => 'queued', 'replayed' => true], 202);
    });

    $this->withHeaders(['Authorization' => 'Basic '.base64_encode('alice:secret'), 'Idempotency-Key' => 'shared-1'])
        ->postJson('https://localhost/email/submissions', emailSubmissionBody())->assertAccepted()
        ->assertJsonPath('node', 'shared');
    expect($receipts)->toHaveCount(2)->and($receipts[0])->toBe($receipts[1]);
    Http::assertSent(fn (ClientRequest $request): bool => str_contains($request->url(), 'south.internal')
        && $request->method() === 'POST');
});

it('rejects reuse of a key with different message bytes', function (): void {
    Http::fake(fn (ClientRequest $request) => Http::response(['id' => $request['receipt_id']], 202));
    $headers = ['Authorization' => 'Basic '.base64_encode('alice:secret'), 'Idempotency-Key' => 'same-key'];
    $this->withHeaders($headers)->postJson('https://localhost/email/submissions', emailSubmissionBody())->assertAccepted();
    $changed = emailSubmissionBody();
    $changed['message_base64'] = base64_encode("From: alice@example.test\r\n\r\nchanged");
    $this->withHeaders($headers)->postJson('https://localhost/email/submissions', $changed)->assertStatus(409);
    Http::assertSentCount(1);
});

it('shares the sender-domain quota across HTTP accounts without charging a rejected account', function (): void {
    app(\Webong\Gateway\Email\EmailSubmissionPolicyStore::class)->create(['domain_daily_limit' => 1]);
    Http::fake(fn (ClientRequest $request) => Http::response(['id' => $request['receipt_id']], 202));
    $first = $this->withHeaders(['Authorization' => 'Basic '.base64_encode('alice:secret'), 'Idempotency-Key' => 'domain-alice'])
        ->postJson('https://localhost/email/submissions', emailSubmissionBody())->assertAccepted()->json('id');
    $bob = emailSubmissionBody('bob@example.test');
    $bob['message_base64'] = base64_encode("From: bob@example.test\r\nSubject: hi\r\n\r\nhello");
    $this->withHeaders(['Authorization' => 'Basic '.base64_encode('bob:secret'), 'Idempotency-Key' => 'domain-bob'])
        ->postJson('https://localhost/email/submissions', $bob)->assertStatus(429);
    $this->withHeaders(['Authorization' => 'Basic '.base64_encode('alice:secret'), 'Idempotency-Key' => 'domain-alice'])
        ->postJson('https://localhost/email/submissions', emailSubmissionBody())->assertAccepted()->assertJsonPath('id', $first);

    expect(DB::table('usage_counters')->where('metric', 'email.submission.account')->where('subject_hash', hash('sha256', 'account-bob'))->exists())->toBeFalse()
        ->and(DB::table('usage_counters')->where('metric', 'email.submission.domain')->where('subject_hash', hash('sha256', 'example.test'))->value('attempts'))->toBe(1);
    Http::assertSentCount(1);
});

it('expires submission keys after a day and prunes their ledger', function (): void {
    Http::fake(fn (ClientRequest $request) => Http::response(['id' => $request['receipt_id']], 202));
    $headers = ['Authorization' => 'Basic '.base64_encode('alice:secret'), 'Idempotency-Key' => 'reuse-tomorrow'];
    try {
        Carbon::setTestNow(Carbon::parse('2026-10-02 10:00:00', 'UTC'));
        $first = $this->withHeaders($headers)->postJson('https://localhost/email/submissions', emailSubmissionBody())->assertAccepted()->json('id');
        Carbon::setTestNow(Carbon::parse('2026-10-03 10:00:01', 'UTC'));
        $this->artisan('gateway:prune-email-submission-usage')->assertSuccessful();
        expect(DB::table('email_http_submissions')->count())->toBe(0);
        $second = $this->withHeaders($headers)->postJson('https://localhost/email/submissions', emailSubmissionBody())->assertAccepted()->json('id');
        expect($second)->not->toBe($first);
        Http::assertSentCount(2);
    } finally {
        Carbon::setTestNow();
    }
});
