<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver;

use Webong\WorkFlow\Contracts\{FlowOperationProvider, FlowStepExecutor};
use Webong\WorkFlow\ValueObjects\{FlowOperation, FlowStepDefinition, FlowStepInvocation, StepResult};

/** Deployed operation implementations; definition metadata can only select a key. */
final readonly class ApprovedOperations implements FlowStepExecutor, FlowOperationProvider
{
    public const KEYS = ['gateway.approval', 'gateway.complete', 'gateway.http.v1'];

    /** @param list<string> $approved */
    public function __construct(private array $approved,private ?GatewayHttpDelivery $http=null)
    {
        if (!array_is_list($approved) || $approved === [] || array_diff($approved,self::KEYS) !== []) {
            throw new \InvalidArgumentException('Unknown or unapproved WorkFlow operation.');
        }
    }

    public function keys(): array {return $this->approved;}

    public function supports(FlowStepDefinition $step): bool
    {
        if(!in_array($step->metadata['operation']??null,$this->approved,true)){return false;}
        return ($step->metadata['operation']??null)!=='gateway.http.v1' || (is_string($step->metadata['destination']??null) && preg_match('/\A[a-z][a-z0-9._-]{0,63}\z/D',$step->metadata['destination'])===1 && is_string($step->metadata['destination_sha256']??null) && preg_match('/\A[a-f0-9]{64}\z/D',$step->metadata['destination_sha256'])===1);
    }

    public function operation(FlowStepDefinition $step): ?FlowOperation
    {
        if (!$this->supports($step)) {return null;}
        $key=$step->metadata['operation'];
        return new FlowOperation($key,match($key){'gateway.approval'=>'Await approval','gateway.http.v1'=>'Gateway HTTP delivery v1',default=>'Complete step'});
    }

    public function execute(FlowStepInvocation $invocation): StepResult
    {
        if (!$this->supports($invocation->step)) {throw new \InvalidArgumentException('Operation is unavailable.');}
        return match($invocation->step->metadata['operation']) {
            'gateway.approval'=>StepResult::deferred('Awaiting an authenticated approval callback'),
            'gateway.complete'=>StepResult::completed(),
            'gateway.http.v1'=>$this->http?->execute($invocation)??StepResult::failed('Private Gateway HTTP delivery is unavailable.',metadata:['error_code'=>'http_bridge_unavailable']),
        };
    }
}
