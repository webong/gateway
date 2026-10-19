<?php

declare(strict_types=1);

use Webong\Gateway\Email\MailNetworkPolicy;

it('allows public mail targets with either network scope', function (): void {
    expect(MailNetworkPolicy::permits('8.8.8.8', 'public'))->toBeTrue()
        ->and(MailNetworkPolicy::permits('8.8.8.8', 'private'))->toBeTrue()
        ->and(MailNetworkPolicy::permits('2606:4700:4700::1111', 'public'))->toBeTrue();
});

it('only allows private mail targets with an explicit private scope', function (): void {
    expect(MailNetworkPolicy::permits('10.0.0.8', 'public'))->toBeFalse()
        ->and(MailNetworkPolicy::permits('10.0.0.8', 'private'))->toBeTrue()
        ->and(MailNetworkPolicy::permits('fd00::8', 'public'))->toBeFalse()
        ->and(MailNetworkPolicy::permits('fd00::8', 'private'))->toBeTrue();
});

it('rejects local and special-use addresses even with private scope', function (): void {
    foreach (['127.0.0.1', '169.254.169.254', '192.0.2.1', '198.18.0.1', '::1',
        'fe80::1', '2001:db8::1'] as $ip) {
        expect(MailNetworkPolicy::permits($ip, 'private'))->toBeFalse();
    }

    expect(MailNetworkPolicy::permits('8.8.8.8', 'unknown'))->toBeFalse();
});
