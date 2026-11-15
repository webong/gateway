<?php
// Private, disposable HTTPS fixture. No published ports or external uploads.
declare(strict_types=1);

$root=$argv[1]??throw new InvalidArgumentException('Fixture root required');
$context=stream_context_create(['ssl'=>[
    'local_cert'=>$root.'/ca.pem','local_pk'=>$root.'/key.pem',
    'verify_peer'=>false, // No client certificates; clients still verify this server.
]]);
$server=stream_socket_server('tls://0.0.0.0:8443',$code,$message,STREAM_SERVER_BIND|STREAM_SERVER_LISTEN,$context);
if($server===false){throw new RuntimeException('Unable to bind fixture TLS server');}
while(true){
    $client=@stream_socket_accept($server,1);
    if($client===false){continue;}
    stream_set_timeout($client,10);
    $request=fgets($client,8192);$agent='';$bytes=0;
    while(($line=fgets($client,8192))!==false && trim($line)!==''){
        $bytes+=strlen($line);if($bytes>16384){break;}
        if(str_starts_with(strtolower($line),'user-agent:')){$agent=trim(substr($line,11));}
    }
    $path=explode(' ',trim($request?:''))[1]??'';
    $file=$root.'/'.basename($path);
    if($path==='/healthz'){
        fwrite($client,"HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nOK");
    }elseif($path==='/workflow.tar.gz'){
        fwrite($client,"HTTP/1.1 302 Found\r\nLocation: https://weaver-artifacts:8443/objects/workflow.tar.gz\r\nContent-Length: 0\r\nConnection: close\r\n\r\n");
    }elseif(($path==='/objects/workflow.tar.gz' || $path==='/packages.json'
        || preg_match('~\A/gateway-weaver-composer-(amd64|arm64)\.tar\.gz\z~D',$path)===1) && is_file($file)){
        $type=str_ends_with($path,'.json')?'application/json':'application/gzip';
        fwrite($client,"HTTP/1.1 200 OK\r\nContent-Type: ".$type."\r\nContent-Length: ".filesize($file)."\r\nConnection: close\r\n\r\n");
        $input=fopen($file,'rb');stream_copy_to_stream($input,$client);fclose($input);
    }else{
        fwrite($client,"HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n");
    }
    fclose($client);
    echo json_encode(['path'=>$path,'agent'=>$agent],JSON_THROW_ON_ERROR|JSON_UNESCAPED_SLASHES)."\n";
    flush();
}
