<?php

declare(strict_types=1);

use Webong\Gateway\Email\EmailRuntimePolicyStore;
use Webong\Gateway\Events\WebEventPolicyStore;

it('manages Email runtime behavior through a versioned Planner resource', function (): void {
    $this->getJson('/email/runtime-policy')->assertUnauthorized();
    $this->withToken('registry-secret')->getJson('/email/runtime-policy')->assertNotFound();
    expect(app(EmailRuntimePolicyStore::class)->current()['http_submission_enabled'])->toBeFalse();

    $created = $this->withToken('registry-secret')->postJson('/email/runtime-policy', [
        'http_submission_enabled' => true, 'connected_smtp_enabled' => true,
        'verification_ttl_seconds' => 0, 'submission_usage_retention_days' => 14,
    ])->assertCreated()->assertHeader('ETag', '"1"');
    expect(app(EmailRuntimePolicyStore::class)->current())->toMatchArray([
        'http_submission_enabled' => true, 'connected_smtp_enabled' => true,
        'verification_ttl_seconds' => 0, 'submission_usage_retention_days' => 14,
    ]);
    $this->withToken('registry-secret')->postJson('/email/runtime-policy', [])->assertConflict();
    $this->withToken('registry-secret')->putJson('/email/runtime-policy', [])->assertStatus(428);
    $this->withToken('registry-secret')->withHeader('If-Match', '"1"')
        ->putJson('/email/runtime-policy', ['dmarc_enforce_enabled' => true, 'enabled' => false])
        ->assertOk()->assertHeader('ETag', '"2"');
    expect(app(EmailRuntimePolicyStore::class)->current()['dmarc_enforce_enabled'])->toBeFalse();
    $this->withToken('registry-secret')->withHeader('If-Match', '"1"')
        ->deleteJson('/email/runtime-policy')->assertStatus(412);
    $this->withToken('registry-secret')->withHeader('If-Match', '"2"')
        ->deleteJson('/email/runtime-policy')->assertNoContent();
    $this->withToken('registry-secret')->postJson('/email/runtime-policy', ['verification_ttl_seconds' => -1])
        ->assertUnprocessable();
    $this->withToken('registry-secret')->postJson('/email/runtime-policy', ['unknown' => true])
        ->assertUnprocessable();

    $history = $this->withToken('registry-secret')->getJson('/configurations/email.runtime-policy/'.$created->json('id').'/revisions')
        ->assertOk()->json('data');
    expect(array_column($history, 'operation'))->toBe(['created', 'replaced', 'deleted']);
});

it('manages Web event capture and retention through a versioned Planner resource', function (): void {
    expect(app(WebEventPolicyStore::class)->current())->toBe(['enabled' => false, 'retention_days' => 30]);
    $created = $this->withToken('registry-secret')->postJson('/web/event-policy', [
        'enabled' => true, 'retention_days' => 7,
    ])->assertCreated()->assertHeader('ETag', '"1"');
    expect(app(WebEventPolicyStore::class)->current())->toBe(['enabled' => true, 'retention_days' => 7]);
    $this->withToken('registry-secret')->withHeader('If-Match', '"1"')
        ->putJson('/web/event-policy', ['enabled' => false, 'retention_days' => 4])
        ->assertOk()->assertHeader('ETag', '"2"');
    expect(app(WebEventPolicyStore::class)->current())->toBe(['enabled' => false, 'retention_days' => 4]);
    $this->withToken('registry-secret')->withHeader('If-Match', '"2"')
        ->deleteJson('/web/event-policy')->assertNoContent();
    $this->withToken('registry-secret')->postJson('/web/event-policy', ['retention_days' => 0])
        ->assertUnprocessable();
    $this->withToken('registry-secret')->postJson('/web/event-policy', ['enabled' => 'true'])
        ->assertUnprocessable();
    $this->withToken('registry-secret')->getJson('/configurations/web.event-policy/'.$created->json('id').'/revisions')
        ->assertOk()->assertJsonCount(3, 'data');
});
