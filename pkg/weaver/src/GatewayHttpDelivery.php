<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver;

use Webong\WorkFlow\ValueObjects\{FlowStepInvocation,StepResult,StepOutput};

/** Only the private parent may select a URL/credential and invoke Gateway HTTP. */
final readonly class GatewayHttpDelivery
{
    public function __construct(private string $url,private string $token,private array $bindings)
    {
        $target=parse_url($url);
        if(!is_array($target) || ($target['scheme']??null)!=='http' || !in_array($target['host']??null,['127.0.0.1','[::1]'],true)
            || ($target['path']??null)!=='/internal/deliver' || isset($target['user']) || strlen($token)<32){throw new \InvalidArgumentException('Private Gateway delivery bridge is unavailable.');}
    }

    /** Scope identifies the provider account/deduplication domain, not a credential revision. */
    public static function fingerprint(string $url,string $scope,string $method='POST',string $header='Idempotency-Key'): string {return hash('sha256',$method."\0".$url."\0".$scope."\0".strtolower($header));}

    public function execute(FlowStepInvocation $invocation): StepResult
    {
        $name=$invocation->step->metadata['destination']??null;
        $pin=$invocation->step->metadata['destination_sha256']??null;
        $binding=$this->bindings[$name]??null;
        $policy=$invocation->step->retryPolicy?->idempotent===true;
        if(!is_array($binding) || ($binding['fingerprint']??null)!==$pin || ($policy && ($binding['idempotent']??false)!==true)){
            return StepResult::failed('HTTP destination binding or replay policy changed; reconcile before continuing.',metadata:['error_code'=>'http_binding_mismatch']);
        }
        $key=$invocation->context->get('_queue')['idempotency_key']??null;
        if(!is_string($key) || preg_match('/\A[a-f0-9]{64}\z/D',$key)!==1){return StepResult::failed('Queue effect identity is unavailable.',metadata:['error_code'=>'effect_identity_missing']);}
        $body=json_encode(['destination'=>$name,'destination_sha256'=>$pin,'idempotency_key'=>$key,'payload'=>(object)$invocation->input->all()],JSON_THROW_ON_ERROR);
        if(strlen($body)>65536){return StepResult::failed('HTTP input exceeds 64 KiB.',metadata:['error_code'=>'http_input_too_large']);}
        $context=stream_context_create(['http'=>['method'=>'POST','timeout'=>23,'ignore_errors'=>true,'header'=>"Content-Type: application/json\r\nAuthorization: Bearer ".$this->token."\r\n",'content'=>$body]]);
        $raw=@file_get_contents($this->url,false,$context,0,8192);
        $result=is_string($raw)?json_decode($raw,true):null;
        if(!is_array($result) || !is_int($result['status']??null) || !is_bool($result['uncertain']??null) || !is_bool($result['retryable']??null)){
            return StepResult::failed('HTTP delivery outcome is unknown; reconcile before replay.',retriable:$policy && $binding['idempotent']===true,metadata:['error_code'=>'http_outcome_uncertain']);
        }
        if(!$result['uncertain'] && $result['status']>=200 && $result['status']<300){
            return StepResult::completed(output:StepOutput::from(['http_status'=>$result['status']]));
        }
        return StepResult::failed($result['uncertain']?'HTTP delivery outcome is unknown; reconcile before replay.':'HTTP provider rejected the delivery.',
            retriable:$policy && $result['retryable'] && $binding['idempotent']===true,
            metadata:['error_code'=>$result['uncertain']?'http_outcome_uncertain':'http_delivery_failed','http_status'=>$result['status']]);
    }
}
