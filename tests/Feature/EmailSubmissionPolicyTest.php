<?php

declare(strict_types=1);

use Webong\Gateway\Email\EmailSubmissionPolicyStore;

it('changes submission quota defaults through the authenticated Planner resource', function (): void {
    $this->postJson('/email/submission-policy', ['daily_limit' => 2])->assertUnauthorized();
    $created = $this->withToken('registry-secret')->postJson('/email/submission-policy', [
        'daily_limit' => 2, 'domain_daily_byte_limit' => 4096,
    ])->assertCreated()->assertHeader('ETag', '"1"')->json();
    expect(app(EmailSubmissionPolicyStore::class)->limits())->toBe([
        'daily_limit' => 2, 'daily_byte_limit' => 0,
        'domain_daily_limit' => 0, 'domain_daily_byte_limit' => 4096,
    ]);
    $this->withToken('registry-secret')->withHeader('If-Match', '"1"')
        ->putJson('/email/submission-policy', ['daily_limit' => 3])
        ->assertOk()->assertHeader('ETag', '"2"');
    $this->withToken('registry-secret')->withHeader('If-Match', '"1"')
        ->deleteJson('/email/submission-policy')->assertStatus(412);
    $this->withToken('registry-secret')->withHeader('If-Match', '"2"')
        ->deleteJson('/email/submission-policy')->assertNoContent();
    expect(app(EmailSubmissionPolicyStore::class)->limits()['daily_limit'])->toBe(0);
    $this->withToken('registry-secret')
        ->getJson('/configurations/email.submission-policy/'.$created['id'].'/revisions')
        ->assertOk()->assertJsonCount(3, 'data');
});
