<?php
// Disposable HTTPS receiver; reachable only on the isolated acceptance network.
declare(strict_types=1);
$context=stream_context_create(['ssl'=>['local_cert'=>'/fixture/ca.pem','local_pk'=>'/fixture/key.pem']]);
$server=stream_socket_server('tls://0.0.0.0:8443',$code,$message,STREAM_SERVER_BIND|STREAM_SERVER_LISTEN,$context);
if($server===false){throw new RuntimeException($message);}
while(true){
    $client=@stream_socket_accept($server,1);
    if($client===false){continue;}
    stream_set_timeout($client,5);
    $request=fgets($client,8192);$headers=[];$size=0;
    while(($line=fgets($client,8192))!==false && trim($line)!==''){
        $size+=strlen($line);if($size>32768){throw new RuntimeException('Oversized fixture headers');}
        [$key,$value]=explode(':',$line,2);$headers[strtolower(trim($key))]=trim($value);
    }
    $length=(int)($headers['content-length']??0);
    if($length<0 || $length>65536){throw new RuntimeException('Oversized fixture body');}
    $body='';while(strlen($body)<$length){$part=fread($client,$length-strlen($body));if($part===false || $part===''){break;}$body.=$part;}
    clearstatcache(true,'/tmp/unavailable');
    $path=explode(' ',trim($request?:''))[1]??'';
    if($path==='/health'){$response='OK';$status='200 OK';}
    elseif($path==='/events' && is_file('/tmp/unavailable')){$response='retry later';$status='503 Service Unavailable';}
    elseif($path==='/events'){
        $payload=json_decode($body,true,flags:JSON_THROW_ON_ERROR);
        file_put_contents('/tmp/received.jsonl',json_encode(['payload'=>$payload,'event_id'=>$headers['x-webhook-forwarder-delivery-id']??null],JSON_THROW_ON_ERROR)."\n",FILE_APPEND|LOCK_EX);
        $response='';$status='204 No Content';
    }elseif($path==='/stream'){
        $last=$headers['last-event-id']??'none';
        fwrite($client,"HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nConnection: close\r\n\r\nid: release-1\ndata: resumed=$last\n\n");
        usleep(100000);
        fwrite($client,"id: release-2\ndata: live-update\n\n");fclose($client);continue;
    }else{$response='';$status='404 Not Found';}
    fwrite($client,"HTTP/1.1 $status\r\nContent-Length: ".strlen($response)."\r\nConnection: close\r\n\r\n$response");
    fclose($client);
}
