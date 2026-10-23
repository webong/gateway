<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

final readonly class DmarcAssessment
{
    public function __construct(
        public string $result,
        public string $policy = 'none',
        public string $policyDomain = '',
        public bool $spfAligned = false,
        public bool $dkimAligned = false,
        public string $effectivePolicy = 'none',
        public bool $testing = false,
    ) {}

    /** @return array<string, string> */
    public function metadata(): array
    {
        return [
            'dmarc_result' => $this->result,
            'dmarc_policy' => $this->policy,
            'dmarc_policy_domain' => $this->policyDomain,
            'dmarc_spf_aligned' => $this->spfAligned ? 'true' : 'false',
            'dmarc_dkim_aligned' => $this->dkimAligned ? 'true' : 'false',
            'dmarc_effective_policy' => $this->effectivePolicy,
            'dmarc_testing' => $this->testing ? 'true' : 'false',
        ];
    }
}
