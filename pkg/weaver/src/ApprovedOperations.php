<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver;

use Webong\WorkFlow\Contracts\{FlowOperationProvider, FlowStepExecutor};
use Webong\WorkFlow\ValueObjects\{FlowOperation, FlowStepDefinition, FlowStepInvocation, StepResult};

/** Deployed operation implementations; definition metadata can only select a key. */
final readonly class ApprovedOperations implements FlowStepExecutor, FlowOperationProvider
{
    public const KEYS = ['gateway.approval', 'gateway.complete'];

    /** @param list<string> $approved */
    public function __construct(private array $approved)
    {
        if (!array_is_list($approved) || $approved === [] || array_diff($approved,self::KEYS) !== []) {
            throw new \InvalidArgumentException('Unknown or unapproved WorkFlow operation.');
        }
    }

    public function supports(FlowStepDefinition $step): bool
    {
        return in_array($step->metadata['operation'] ?? null,$this->approved,true);
    }

    public function operation(FlowStepDefinition $step): ?FlowOperation
    {
        if (!$this->supports($step)) {return null;}
        $key=$step->metadata['operation'];
        return new FlowOperation($key,$key === 'gateway.approval' ? 'Await approval' : 'Complete step');
    }

    public function execute(FlowStepInvocation $invocation): StepResult
    {
        if (!$this->supports($invocation->step)) {throw new \InvalidArgumentException('Operation is unavailable.');}
        return match($invocation->step->metadata['operation']) {
            'gateway.approval'=>StepResult::deferred('Awaiting an authenticated approval callback'),
            'gateway.complete'=>StepResult::completed(),
        };
    }
}
