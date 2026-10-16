<?php

declare(strict_types=1);

use Webong\Gateway\Email\EmailUpstreamCatalog;

it('stores typed upstreams in shared configurations and enforces the deployed host ceiling', function (): void {
    config()->set('gateway.email.upstream_allowed_hosts', ['imap.provider.test', 'smtp.provider.test']);
    $this->postJson('/email/upstreams', [])->assertUnauthorized();
    $this->withToken('registry-secret')->postJson('/email/upstreams', [
        'protocol' => 'imap', 'name' => 'primary', 'address' => 'other.test:993',
        'server_name' => 'other.test',
    ])->assertUnprocessable();
    $created = $this->withToken('registry-secret')->postJson('/email/upstreams', [
        'protocol' => 'imap', 'name' => 'primary', 'address' => 'imap.provider.test:993',
        'server_name' => 'imap.provider.test',
    ])->assertCreated();
    $catalog = app(EmailUpstreamCatalog::class);
    expect($catalog->active('imap', 'primary')['address'])->toBe('imap.provider.test:993');
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
