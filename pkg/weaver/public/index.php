<?php

declare(strict_types=1);

use Webong\Gateway\Weaver\{RuntimeConfiguration,RuntimeFactory,RuntimeControl,RpcFailure};

require_once dirname(__DIR__).'/bin/bootstrap.php';
header('Content-Type: application/json');
try {
    $body=file_get_contents('php://input');
    $request=json_decode($body===false?'':$body,true,128,JSON_THROW_ON_ERROR);
    if (!is_array($request) || !is_string($request['method'] ?? null) || !is_array($request['params'] ?? null)) {
        throw new RpcFailure(-32602,'Invalid params');
    }
    $path=getenv('GATEWAY_WEAVER_CONTEXT');
    if (!is_string($path) || $path==='') {throw new RuntimeException('Private runtime context is required.');}
    $context=json_decode(file_get_contents($path),true,128,JSON_THROW_ON_ERROR);
    $configuration=new RuntimeConfiguration($context['configuration']);
    $configuration->assertAuthentication($context['authentication']);
    $runtime=new RuntimeFactory($configuration,$context['database'],$context);
    if ($request['method']==='workflow.ready') {
        $response=['result'=>['ready'=>true]];
    } elseif (($request['internal'] ?? false) === true) {
        $response=['result'=>(new RuntimeControl($runtime))->invoke($request['method'],$request['params'])];
    } else {
        $principal=$request['principal'] ?? null;
        if (!is_string($principal) || $principal==='') {throw new RpcFailure(-32003,'Forbidden');}
        $response=['result'=>$runtime->application($principal)->invoke($principal,$request['method'],$request['params'])];
    }
} catch(RpcFailure $error) {
    $response=['error'=>['code'=>$error->rpcCode,'message'=>$error->getMessage()]];
} catch(InvalidArgumentException|JsonException) {
    $response=['error'=>['code'=>-32602,'message'=>'Invalid params']];
} catch(Throwable) {
    error_log('WorkFlow RPC/storage invocation failed.');
    $response=['error'=>['code'=>-32603,'message'=>'Internal error']];
}
echo json_encode($response,JSON_THROW_ON_ERROR);
