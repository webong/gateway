<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver;

/** An explicit run-control grant, never inferred from account ownership. */
final readonly class RunGrant
{
    public const ACTIONS = ['flow.definition', 'flow.get', 'flow.start', 'flow.complete', 'flow.resume', 'flow.cancel', 'flow.operations', 'flow.validate'];

    /** @param list<string> $actions */
    public function __construct(
        public string $account,
        public string $flowKey,
        public string $subjectType,
        public string $subjectId,
        public array $actions,
    ) {
        foreach ([$account, $flowKey, $subjectType, $subjectId] as $value) {
            if ($value === '' || strlen($value) > 255 || $value === '*') {
                throw new \InvalidArgumentException('Run grants require explicit account, flow and subject identities.');
            }
        }
        if ($actions === [] || ! array_is_list($actions) || array_diff($actions, self::ACTIONS) !== []) {
            throw new \InvalidArgumentException('Invalid WorkFlow grant actions.');
        }
    }

    /** @param array<string, mixed> $params */
    public function permits(string $account, string $method, array $params): bool
    {
        return $account === $this->account && $this->flowKey === ($params['flow_key'] ?? null)
            && is_array($params['subject'] ?? null)
            && $this->subjectType === ($params['subject']['type'] ?? null)
            && $this->subjectId === ($params['subject']['id'] ?? null)
            && in_array($method, $this->actions, true);
    }

    /** @param array<string, mixed> $document */
    public static function fromArray(array $document): self
    {
        if (array_diff(array_keys($document), ['account', 'flow_key', 'subject', 'actions']) !== []) {
            throw new \InvalidArgumentException('Unsupported WorkFlow grant properties.');
        }
        $subject = $document['subject'] ?? null;
        if (! is_array($subject) || array_diff(array_keys($subject), ['type', 'id']) !== []) {
            throw new \InvalidArgumentException('Invalid WorkFlow grant subject.');
        }
        foreach ([$document['account'] ?? null, $document['flow_key'] ?? null, $subject['type'] ?? null, $subject['id'] ?? null] as $value) {
            if (! is_string($value)) { throw new \InvalidArgumentException('Invalid WorkFlow grant identity.'); }
        }
        $actions = $document['actions'] ?? null;
        if (! is_array($actions) || array_filter($actions, static fn ($value) => ! is_string($value)) !== []) {
            throw new \InvalidArgumentException('Invalid WorkFlow grant actions.');
        }
        return new self($document['account'], $document['flow_key'], $subject['type'], $subject['id'], $actions);
    }

    /** @return array<string, mixed> */
    public function toArray(): array
    {
        return ['account' => $this->account, 'flow_key' => $this->flowKey,
            'subject' => ['type' => $this->subjectType, 'id' => $this->subjectId], 'actions' => $this->actions];
    }
}
