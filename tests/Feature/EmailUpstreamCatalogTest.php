<?php

declare(strict_types=1);

use Webong\Gateway\Email\EmailUpstreamCatalog;

it('stores typed upstreams and their network scope without a deployed host list', function (): void {
    $this->postJson('/email/upstreams', [])->assertUnauthorized();
    $this->withToken('registry-secret')->postJson('/email/upstreams', [
        'protocol' => 'imap', 'name' => 'primary', 'address' => 'other.test:0',
        'server_name' => 'other.test',
    ])->assertUnprocessable();
    $created = $this->withToken('registry-secret')->postJson('/email/upstreams', [
        'protocol' => 'imap', 'name' => 'primary', 'address' => 'imap.provider.test:993',
        'server_name' => 'imap.provider.test',
    ])->assertCreated();
    $catalog = app(EmailUpstreamCatalog::class);
    expect($catalog->active('imap', 'primary')['address'])->toBe('imap.provider.test:993')
        ->and($catalog->active('imap', 'primary')['network_scope'])->toBe('public');
    $this->withToken('registry-secret')->postJson('/email/upstreams', [
        'protocol' => 'smtp', 'name' => 'internal', 'address' => 'mail.internal:465',
        'server_name' => 'mail.internal', 'tls_mode' => 'implicit', 'network_scope' => 'private',
    ])->assertCreated()->assertJsonPath('network_scope', 'private');
    $this->withToken('registry-secret')->postJson('/email/upstreams', [
        'protocol' => 'imap', 'name' => 'primary', 'address' => 'imap.provider.test:993',
        'server_name' => 'imap.provider.test',
    ])->assertConflict();
    $this->withToken('registry-secret')->putJson('/email/upstreams/'.$created->json('id'), [
        'protocol' => 'imap', 'name' => 'primary', 'address' => 'imap.provider.test:993',
        'server_name' => 'imap.provider.test', 'enabled' => false,
    ])->assertStatus(428);
    $disabled = $this->withToken('registry-secret')->withHeaders(['If-Match' => $created->headers->get('ETag')])
        ->putJson('/email/upstreams/'.$created->json('id'), [
            'protocol' => 'imap', 'name' => 'primary', 'address' => 'imap.provider.test:993',
            'server_name' => 'imap.provider.test', 'enabled' => false,
        ])->assertOk();
    expect($catalog->active('imap', 'primary'))->toBeNull();
    $this->withToken('registry-secret')->withHeaders(['If-Match' => $disabled->headers->get('ETag')])
        ->deleteJson('/email/upstreams/'.$created->json('id'))->assertNoContent();
});
