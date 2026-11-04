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

it('rejects special-use space that PHP does not classify as reserved', function (): void {
    foreach (['0.1.2.3', '100.64.0.1', '100.100.100.200', '198.18.0.1', '203.0.113.9', '224.0.0.1', '240.0.0.1',
        '192.88.99.2', '::10.0.0.1', '100::1', '100:0:0:1::1', '2001:2::1', '2001:4::1', '2001:5::1', '3fff::1', '5f00::1', 'ff02::1', '2001::1'] as $ip) {
        expect(MailNetworkPolicy::permits($ip, 'public'))->toBeFalse($ip)
            ->and(MailNetworkPolicy::permits($ip, 'private'))->toBeFalse($ip);
    }
});

it('permits the globally reachable exceptions within IETF IPv6 assignments', function (): void {
    foreach (['2001:1::1', '2001:1::2', '2001:1::3', '2001:3::1', '2001:4:112::1', '2001:20::1', '2001:30::1'] as $ip) {
        expect(MailNetworkPolicy::permits($ip, 'public'))->toBeTrue($ip);
    }
});

it('judges IPv4-mapped, NAT64, and 6to4 addresses by the IPv4 address they carry', function (): void {
    foreach (['::ffff:8.8.8.8', '64:ff9b::808:808', '2002:808:808::1'] as $ip) {
        expect(MailNetworkPolicy::permits($ip, 'public'))->toBeTrue($ip)
            ->and(MailNetworkPolicy::permits($ip, 'private'))->toBeTrue($ip);
    }

    foreach (['::ffff:10.0.0.1', '64:ff9b::a00:1', '2002:a00:1::1'] as $ip) {
        expect(MailNetworkPolicy::permits($ip, 'public'))->toBeFalse($ip)
            ->and(MailNetworkPolicy::permits($ip, 'private'))->toBeTrue($ip);
    }

    foreach (['::ffff:127.0.0.1', '::ffff:169.254.169.254', '::ffff:100.100.100.200', '64:ff9b::a9fe:a9fe',
        '2002:a9fe:a9fe::1'] as $ip) {
        expect(MailNetworkPolicy::permits($ip, 'public'))->toBeFalse($ip)
            ->and(MailNetworkPolicy::permits($ip, 'private'))->toBeFalse($ip);
    }
});

it('rejects malformed addresses', function (): void {
    foreach (['', 'example.com', '8.8.8', '10.0.0.1/8', 'fe80::1%eth0'] as $ip) {
        expect(MailNetworkPolicy::permits($ip, 'private'))->toBeFalse($ip);
    }
});
