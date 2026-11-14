<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver;

use Webong\WorkFlow\ValueObjects\{FlowState,FlowStateSubject};

/** Explicit offline imports retain legacy queue IDs without weakening account scope. */
final class ImportedRunIdentity
{
    public static function resolve(FlowStateSubject $subject, FlowState $reservation): ?string
    {
        if(!array_key_exists('_rpc_import',$reservation->metadata)){return null;}
        $marker=$reservation->metadata['_rpc_import'];
        if(!is_array($marker) || count($marker)!==3 || array_diff(array_keys($marker),['version','account','subject'])!==[] || $marker['version']!==1
            || !is_string($marker['account']) || !is_array($marker['subject'])
            || count($marker['subject'])!==2 || array_diff(array_keys($marker['subject']),['type','id'])!==[] || !is_string($marker['subject']['type']) || !is_string($marker['subject']['id'])) {
            throw new RpcFailure(-32009,'Invalid imported queue identity');
        }
        $original=new FlowStateSubject($marker['subject']['type'],$marker['subject']['id']);
        $scoped=RpcRunIdentity::accountSubject($marker['account'],$original);
        $run=$reservation->run ?? throw new RpcFailure(-32009,'Imported run is missing');
        $key=RpcRunIdentity::executionId($original,$run->definition->key,$run->id);
        if($subject->type!==$scoped->type || $subject->id!==$scoped->id
            || ($reservation->metadata['_rpc_authority']??null)!=='queue-v1'
            || ($reservation->metadata['execution_id']??null)!==$key) {
            throw new RpcFailure(-32009,'Imported queue routing scope does not match this request');
        }
        return $key;
    }
}
