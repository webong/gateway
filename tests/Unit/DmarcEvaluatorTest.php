<?php

declare(strict_types=1);

use Webong\Gateway\Email\DmarcEvaluator;
use Webong\Gateway\Email\TxtResolver;
use Webong\Gateway\Protocol\EventKind;
use Webong\Gateway\Protocol\GatewayEvent;
use Webong\Gateway\Protocol\Protocol;

final class DmarcTestTxtResolver implements TxtResolver
{
    /** @var array<string, list<string>> */
    public array $answers = [];

    /** @var list<string> */
    public array $queries = [];

    public bool $unavailable = false;

    public ?string $failName = null;

    /** @return list<string> */
    public function records(string $name): array
    {
        $this->queries[] = $name;
        if ($this->unavailable || $this->failName === $name) {
            throw new RuntimeException('temporary DNS failure');
        }

        return $this->answers[$name] ?? [];
    }
}

/** @param array<string, string> $attributes */
function dmarcEvent(array $attributes, string $id = 'smtp-dmarc-test'): GatewayEvent
{
    return new GatewayEvent(
        id: $id, protocol: Protocol::SMTP, kind: EventKind::TRANSACTION,
        route: 'inbox@managed.test', attributes: $attributes,
    );
}

it('uses a valid aligned DKIM signature even when SPF fails', function (): void {
    $txt = new DmarcTestTxtResolver();
    $txt->answers['_dmarc.sender.example'] = ['v=DMARC1; p=reject; aspf=s; adkim=s'];
    $assessment = (new DmarcEvaluator($txt))->evaluate(dmarcEvent([
        'rfc5322_from_domain' => 'sender.example',
        'rfc5322_from_organizational_domain' => 'sender.example',
        'mail_from' => 'alice@other.example',
        'spf_result' => 'fail',
        'dkim_result' => 'pass',
        'dkim_signatures' => '[{"domain":"other.example","organizational_domain":"other.example","result":"fail"},{"domain":"sender.example","organizational_domain":"sender.example","result":"pass"}]',
    ]));

    expect($assessment->result)->toBe('pass')
        ->and($assessment->policy)->toBe('reject')
        ->and($assessment->spfAligned)->toBeFalse()
        ->and($assessment->dkimAligned)->toBeTrue();
});

it('keeps an aligned SPF pass when an independent DKIM alignment lookup fails', function (): void {
    $txt = new DmarcTestTxtResolver();
    $txt->answers['_dmarc.sender.example'] = ['v=DMARC1; p=reject'];
    $txt->failName = '_dmarc.bad.example';
    $base = [
        'rfc5322_from_domain' => 'sender.example',
        'mail_from' => 'alice@sender.example',
        'spf_result' => 'pass',
        'spf_identity' => 'mailfrom',
        'spf_domain' => 'sender.example',
        'dkim_result' => 'pass',
        'dkim_signatures' => '[{"domain":"bad.example","result":"pass"}]',
    ];

    $assessment = (new DmarcEvaluator($txt))->evaluate(dmarcEvent($base));
    expect($assessment->result)->toBe('pass')
        ->and($assessment->spfAligned)->toBeTrue();

    $assessment = (new DmarcEvaluator($txt))->evaluate(dmarcEvent([
        ...$base,
        'spf_result' => 'fail',
    ]));
    expect($assessment->result)->toBe('temperror');
});

it('uses DNS-tree-derived relaxed alignment and organizational subdomain policy', function (): void {
    $txt = new DmarcTestTxtResolver();
    $txt->answers['_dmarc.example.co.uk'] = ['v=DMARC1; p=none; sp=reject; aspf=r'];
    $assessment = (new DmarcEvaluator($txt))->evaluate(dmarcEvent([
        'rfc5322_from_domain' => 'alerts.example.co.uk',
        'rfc5322_from_organizational_domain' => 'example.co.uk',
        'mail_from' => 'alice@bounce.example.co.uk',
        'spf_result' => 'pass',
        'spf_identity' => 'mailfrom',
        'spf_domain' => 'bounce.example.co.uk',
        'spf_organizational_domain' => 'example.co.uk',
        'dkim_result' => 'none',
    ]));

    expect($assessment->result)->toBe('pass')
        ->and($assessment->policy)->toBe('reject')
        ->and($assessment->policyDomain)->toBe('example.co.uk')
        ->and($assessment->spfAligned)->toBeTrue();
});

it('distinguishes failed alignment, temporary evidence, no policy, and invalid records', function (): void {
    $txt = new DmarcTestTxtResolver();
    $txt->answers['_dmarc.sender.example'] = ['v=DMARC1; p=reject'];
    $evaluator = new DmarcEvaluator($txt);
    $base = [
        'rfc5322_from_domain' => 'sender.example',
        'rfc5322_from_organizational_domain' => 'sender.example',
        'spf_result' => 'fail',
        'dkim_result' => 'fail',
    ];
    expect($evaluator->evaluate(dmarcEvent($base))->result)->toBe('fail')
        ->and($evaluator->evaluate(dmarcEvent([...$base, 'dkim_result' => 'temperror']))->result)->toBe('temperror')
        ->and($evaluator->evaluate(dmarcEvent([...$base, 'dkim_truncated' => 'true']))->result)->toBe('temperror');

    $txt->answers['_dmarc.sender.example'] = [];
    expect($evaluator->evaluate(dmarcEvent($base))->result)->toBe('none');
    $txt->answers['_dmarc.sender.example'] = ['v=DMARC1; p=reject', 'v=DMARC1; p=none'];
    expect($evaluator->evaluate(dmarcEvent($base))->result)->toBe('none');
    $txt->answers['_dmarc.sender.example'] = ['v=DMARC1; p=invalid'];
    expect($evaluator->evaluate(dmarcEvent($base))->result)->toBe('none');
    $txt->answers['_dmarc.sender.example'] = ['v=DMARC1; rua=mailto:reports@sender.example; p=reject'];
    expect($evaluator->evaluate(dmarcEvent($base))->policy)->toBe('reject');
    $txt->answers['_dmarc.sender.example'] = ['v=DMARC1; rua=mailto:reports@sender.example'];
    expect($evaluator->evaluate(dmarcEvent($base))->policy)->toBe('none');
    $txt->answers['_dmarc.sender.example'] = ['V = DMARC1; p=reject'];
    expect($evaluator->evaluate(dmarcEvent($base))->policy)->toBe('reject');
    $txt->unavailable = true;
    expect($evaluator->evaluate(dmarcEvent($base))->result)->toBe('temperror');
});

it('discovers delegated and organizational DMARC policy by bounded DNS tree walk', function (): void {
    $txt = new DmarcTestTxtResolver();
    $txt->answers['_dmarc.mail.department.example.test'] = ['v=DMARC1; p=reject'];
    $txt->answers['_dmarc.department.example.test'] = ['v=DMARC1; p=quarantine; sp=reject; psd=n'];
    $txt->answers['_dmarc.example.test'] = ['v=DMARC1; p=none; psd=n'];
    $event = dmarcEvent([
        'rfc5322_from_domain' => 'alerts.mail.department.example.test',
        'rfc5322_from_organizational_domain' => 'example.test',
        'spf_result' => 'fail',
        'dkim_result' => 'none',
    ]);

    $assessment = (new DmarcEvaluator($txt))->evaluate($event);
    expect($assessment->policyDomain)->toBe('department.example.test')
        ->and($assessment->policy)->toBe('reject')
        ->and($txt->queries)->toBe([
            '_dmarc.alerts.mail.department.example.test',
            '_dmarc.mail.department.example.test',
            '_dmarc.department.example.test',
        ]);

    $txt->queries = [];
    $txt->answers['_dmarc.department.example.test'] = [];
    $assessment = (new DmarcEvaluator($txt))->evaluate($event);
    expect($assessment->policyDomain)->toBe('example.test')
        ->and($assessment->policy)->toBe('none');
});

it('does not mistake a sibling PSL domain for a delegated relaxed-alignment pass', function (): void {
    $txt = new DmarcTestTxtResolver();
    $txt->answers['_dmarc.department.example.test'] = ['v=DMARC1; p=reject; psd=n'];
    $evaluator = new DmarcEvaluator($txt);
    $base = [
        'rfc5322_from_domain' => 'alerts.department.example.test',
        // Spinner's historical PSL hint is deliberately too broad here.
        'rfc5322_from_organizational_domain' => 'example.test',
        'spf_result' => 'pass',
        'spf_identity' => 'mailfrom',
        'spf_organizational_domain' => 'example.test',
        'dkim_result' => 'none',
    ];

    $sibling = $evaluator->evaluate(dmarcEvent([
        ...$base,
        'mail_from' => 'sender@bounce.sibling.example.test',
        'spf_domain' => 'bounce.sibling.example.test',
    ]));
    expect($sibling->result)->toBe('fail')
        ->and($sibling->spfAligned)->toBeFalse();

    $delegated = $evaluator->evaluate(dmarcEvent([
        ...$base,
        'mail_from' => 'sender@bounce.department.example.test',
        'spf_domain' => 'bounce.department.example.test',
    ]));
    expect($delegated->result)->toBe('pass')
        ->and($delegated->spfAligned)->toBeTrue();
});

it('prefers an organizational policy over a public-suffix fallback policy', function (): void {
    $txt = new DmarcTestTxtResolver();
    $txt->answers['_dmarc.example.test'] = ['v=DMARC1; p=reject'];
    $txt->answers['_dmarc.test'] = ['v=DMARC1; p=quarantine; psd=y'];
    $event = dmarcEvent([
        'rfc5322_from_domain' => 'mail.example.test',
        'spf_result' => 'fail',
        'dkim_result' => 'none',
    ]);

    $assessment = (new DmarcEvaluator($txt))->evaluate($event);
    expect($assessment->policyDomain)->toBe('example.test')
        ->and($assessment->policy)->toBe('reject');

    $txt->answers['_dmarc.example.test'] = [];
    $assessment = (new DmarcEvaluator($txt))->evaluate($event);
    expect($assessment->policyDomain)->toBe('test')
        ->and($assessment->policy)->toBe('quarantine');
});

it('applies np only on trusted NXDOMAIN evidence and defers uncertain existence', function (): void {
    $txt = new DmarcTestTxtResolver();
    $txt->answers['_dmarc.example.test'] = ['v=DMARC1; p=none; sp=quarantine; np=reject; psd=n'];
    $evaluator = new DmarcEvaluator($txt);
    $base = [
        'rfc5322_from_domain' => 'missing.example.test',
        'spf_result' => 'fail',
        'dkim_result' => 'none',
    ];

    $missing = $evaluator->evaluate(dmarcEvent([...$base, 'rfc5322_from_existence' => 'nxdomain']));
    expect($missing->result)->toBe('fail')
        ->and($missing->policy)->toBe('reject');

    $existing = $evaluator->evaluate(dmarcEvent([...$base, 'rfc5322_from_existence' => 'exists']));
    expect($existing->result)->toBe('fail')
        ->and($existing->policy)->toBe('quarantine');

    $unknown = $evaluator->evaluate(dmarcEvent($base));
    expect($unknown->result)->toBe('temperror')
        ->and($unknown->policy)->toBe('quarantine');
});

it('never performs more than eight DMARC policy lookups for a deep author domain', function (): void {
    $txt = new DmarcTestTxtResolver();
    $domain = 'a.b.c.d.e.f.g.h.i.j.example.test';
    $assessment = (new DmarcEvaluator($txt))->evaluate(dmarcEvent([
        'rfc5322_from_domain' => $domain,
        'rfc5322_from_organizational_domain' => 'example.test',
        'spf_result' => 'fail',
        'dkim_result' => 'none',
    ]));

    expect($assessment->result)->toBe('none')
        ->and($txt->queries)->toHaveCount(8)
        ->and($txt->queries[0])->toBe('_dmarc.'.$domain)
        ->and($txt->queries[1])->toBe('_dmarc.f.g.h.i.j.example.test');
});

it('uses the testing tag to lower the requested policy and ignores obsolete pct', function (): void {
    $txt = new DmarcTestTxtResolver();
    $txt->answers['_dmarc.sender.example'] = ['v=DMARC1; p=reject; t=y; pct=0'];
    $event = dmarcEvent([
        'rfc5322_from_domain' => 'sender.example',
        'rfc5322_from_organizational_domain' => 'sender.example',
        'spf_result' => 'fail',
        'dkim_result' => 'none',
    ]);
    $assessment = (new DmarcEvaluator($txt))->evaluate($event);

    expect($assessment->result)->toBe('fail')
        ->and($assessment->policy)->toBe('reject')
        ->and($assessment->effectivePolicy)->toBe('quarantine')
        ->and($assessment->testing)->toBeTrue();

    $txt->answers['_dmarc.sender.example'] = ['v=DMARC1; p=quarantine; t=y'];
    expect((new DmarcEvaluator($txt))->evaluate($event)->effectivePolicy)->toBe('none');

    $txt->answers['_dmarc.sender.example'] = ['v=DMARC1; p=reject; pct=0'];
    expect((new DmarcEvaluator($txt))->evaluate($event)->effectivePolicy)->toBe('reject');
});
