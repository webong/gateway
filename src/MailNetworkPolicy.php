<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

/** The Planner's credential probe follows Spinner's public/private mail policy. */
final class MailNetworkPolicy
{
    public static function permits(string $ip, string $scope): bool
    {
        if (! in_array($scope, ['public', 'private'], true)) { return false; }
        $flags = FILTER_FLAG_NO_RES_RANGE | ($scope === 'public' ? FILTER_FLAG_NO_PRIV_RANGE : 0);
        if (filter_var($ip, FILTER_VALIDATE_IP, $flags) === false) { return false; }
        if (filter_var($ip, FILTER_VALIDATE_IP, FILTER_FLAG_IPV4) !== false) {
            foreach (['100.64.0.0/10', '192.0.0.0/24', '192.0.2.0/24', '198.18.0.0/15',
                '198.51.100.0/24', '203.0.113.0/24', '240.0.0.0/4'] as $cidr) {
                [$base, $bits] = explode('/', $cidr);
                $mask = -1 << (32 - (int) $bits);
                if ((ip2long($ip) & $mask) === (ip2long($base) & $mask)) { return false; }
            }
        } elseif (filter_var($ip, FILTER_VALIDATE_IP, FILTER_FLAG_IPV6) !== false) {
            $prefix = substr(bin2hex(inet_pton($ip)), 0, 8);
            if ($prefix === '20010000' || $prefix === '20010db8') { return false; }
        }
        return true;
    }
}
