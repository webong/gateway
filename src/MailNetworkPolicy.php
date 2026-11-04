<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

/**
 * The Planner's credential probe follows Spinner's public/private mail policy,
 * which is Arranger's `integration.EgressAddressAllowed`.
 */
final class MailNetworkPolicy
{
    public static function permits(string $ip, string $scope): bool
    {
        if (! in_array($scope, ['public', 'private'], true)) { return false; }
        if (filter_var($ip, FILTER_VALIDATE_IP) === false) { return false; }
        $packed = inet_pton($ip);
        if (strlen($packed) === 16 && ($embedded = self::embeddedIpv4($packed)) !== null) {
            return self::permits($embedded, $scope);
        }
        $flags = FILTER_FLAG_NO_RES_RANGE | ($scope === 'public' ? FILTER_FLAG_NO_PRIV_RANGE : 0);
        if (filter_var($ip, FILTER_VALIDATE_IP, $flags) === false) { return false; }
        if (strlen($packed) === 4) {
            foreach (['0.0.0.0/8', '100.64.0.0/10', '192.0.0.0/24', '192.0.2.0/24', '198.18.0.0/15',
                '198.51.100.0/24', '203.0.113.0/24', '224.0.0.0/4', '240.0.0.0/4'] as $cidr) {
                [$base, $bits] = explode('/', $cidr);
                $mask = -1 << (32 - (int) $bits);
                if ((ip2long($ip) & $mask) === (ip2long($base) & $mask)) { return false; }
            }
            return true;
        }
        $hex = bin2hex($packed);
        foreach (['00', '0100000000000000', '20010000', '20010db8', '3fff0', 'ff'] as $blocked) {
            // ::/8, 100::/64, 2001::/32, 2001:db8::/32, 3fff::/20, ff00::/8
            if (str_starts_with($hex, $blocked)) { return false; }
        }
        return true;
    }

    /** IPv4-mapped, NAT64 (64:ff9b::/96), and 6to4 (2002::/16) addresses are judged by the IPv4 address they carry. */
    private static function embeddedIpv4(string $packed): ?string
    {
        $hex = bin2hex($packed);
        $octets = match (true) {
            str_starts_with($hex, '00000000000000000000ffff'),
            str_starts_with($hex, '0064ff9b0000000000000000') => substr($packed, 12, 4),
            str_starts_with($hex, '2002') => substr($packed, 2, 4),
            default => null,
        };

        return $octets === null ? null : inet_ntop($octets);
    }
}
