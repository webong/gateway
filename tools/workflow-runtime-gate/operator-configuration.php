<?php
// Representative operator data only; both sides use the same declared operation contract.
$config=json_decode(file_get_contents('/gate/weaver.configuration.json'),true,128,JSON_THROW_ON_ERROR);
$definitions=array_column($config['accounts'][0]['definitions'],null,'key');
$approve=$definitions['gate_order']['steps'][0];
$deliver=$definitions['gate_http']['steps'][0];$deliver['depends_on']=['approve'];
$config['accounts']=[['id'=>'gate-account','definitions'=>[['key'=>'operator_order','version'=>2,'steps'=>[
 $approve,$deliver,['id'=>'finish-v2','depends_on'=>['deliver'],'metadata'=>['operation'=>'gateway.complete']],
]]],'grants'=>[['account'=>'gate-account','flow_key'=>'operator_order','subject'=>['type'=>'order','id'=>'operator-subject'],
 'actions'=>['flow.definition','flow.start','flow.get','flow.complete','flow.resume','flow.cancel','flow.operations']]]]];
$config['http_destinations']=['orders.v1'=>'weaver-http-orders'];
return $config;
