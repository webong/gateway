"""Exercise the extracted Go/PHP bridge and worker, using only disposable state."""
import copy, json, pathlib, secrets, subprocess, sys, tempfile, time, uuid
from urllib.request import Request, urlopen
from urllib.error import HTTPError, URLError

image=sys.argv[1]
name='gateway-workflow-http-'+uuid.uuid4().hex[:10]
volume=name+'-state'
root=pathlib.Path(__file__).resolve().parents[1]
context_path='/state/context.json'
containers=[]

def docker(*args):
    result=subprocess.run(['docker',*args],capture_output=True,text=True)
    if result.returncode:raise AssertionError('Docker operation failed: '+args[0])
    return result.stdout.strip()

def request(path,token=None,method=None,params=None):
    headers={'Content-Type':'application/json'}
    if token:headers['Authorization']='Bearer '+token
    data=None if method is None else json.dumps({'jsonrpc':'2.0','id':37,'method':method,'params':params}).encode()
    try:
        with urlopen(Request(base+path,data=data,headers=headers),timeout=5) as response:
            body=response.read();return response.status,json.loads(body) if body else None
    except HTTPError as error:return error.code,error.read()

def rpc(account,method,params):
    status,response=request('/rpc',tokens[account],method,params)
    assert status==200 and response['id']==37
    assert 'error' not in response,response.get('error')
    return response['result']

def worker():
    docker('exec',name,'php','/extension/bin/worker','--once')

try:
    configuration=json.loads((root/'examples/approval-order.configuration.json').read_text())
    second=copy.deepcopy(configuration['accounts'][0]);second['id']='other-account'
    for grant in second['grants']:grant['account']='other-account'
    configuration['accounts'].append(second)
    tokens={account['id']:secrets.token_urlsafe(32) for account in configuration['accounts']}
    context={'configuration':configuration,'database':{'dsn':'sqlite:/state/workflow.sqlite'},'authentication':tokens}
    docker('volume','create',volume)
    setup=name+'-setup'
    docker('create','--name',setup,'--mount','type=volume,source='+volume+',target=/state',
           '-e','GATEWAY_WEAVER_CONTEXT='+context_path,'--entrypoint','sh',image,'-c',
           'php /extension/bin/migrate --install && chmod 0600 /state/context.json && chown -R 10001:10001 /state')
    containers.append(setup)
    with tempfile.TemporaryDirectory(prefix=name) as directory:
        path=pathlib.Path(directory)/'context.json';path.write_text(json.dumps(context))
        docker('cp',str(path),setup+':'+context_path)
    docker('start','-a',setup);docker('rm',setup);containers.remove(setup)
    docker('run','-d','--name',name,'--user','10001:10001',
           '--mount','type=volume,source='+volume+',target=/state','-p','127.0.0.1::8080',
           '-e','GATEWAY_WEAVER_CONTEXT='+context_path,
           '-e','GATEWAY_WEAVER_RPC_LISTEN=0.0.0.0:8080',
           '-e','GATEWAY_WEAVER_PHP_PUBLIC=/extension/public',
           '--entrypoint','/usr/local/bin/gateway-workflow-bridge',image)
    containers.append(name)
    port=docker('port',name,'8080/tcp').split(':')[-1];base='http://127.0.0.1:'+port
    until=time.monotonic()+45
    while True:
        try:
            if request('/healthz')[0]==204:break
        except (URLError,TimeoutError,ConnectionError):pass
        assert time.monotonic()<until,'Bridge readiness failed'
        time.sleep(.1)
    params={'flow_key':'order-approval','run_id':'same-logical-run','subject':{'type':'order','id':'42'}}
    assert request('/rpc','wrong','flow.start',params)[0]==401
    status,denied=request('/rpc',tokens['example-account'],'flow.start',{**params,'subject':{'type':'order','id':'other'}})
    assert status==200 and denied['error']['code']==-32003
    status,denied=request('/rpc',tokens['example-account'],'flow.start',{**params,'account':'other-account'})
    assert status==200 and denied['error']['code']==-32003
    first=rpc('example-account','flow.start',params)['state'];worker()
    second=rpc('other-account','flow.start',params)['state'];worker()
    assert first['metadata']['execution_id']!=second['metadata']['execution_id']
    waiting=rpc('example-account','flow.get',params)['state']
    assert waiting['steps']['approve']['metadata']['deferred']
    completion={**params,'step_id':'approve','attempt':waiting['steps']['approve']['attempts'],
                'idempotency_key':'callback-before-restart','result':{'status':'completed'}}
    rpc('example-account','flow.complete',completion);rpc('example-account','flow.complete',completion);worker()
    assert rpc('example-account','flow.get',params)['state']['status']=='completed'
    assert rpc('other-account','flow.cancel',params)['state']['status']=='cancelled'
    docker('restart','--timeout','10',name)
    port=docker('port',name,'8080/tcp').split(':')[-1];base='http://127.0.0.1:'+port
    until=time.monotonic()+45
    while True:
        try:
            if request('/healthz')[0]==204:break
        except (URLError,TimeoutError,ConnectionError):pass
        assert time.monotonic()<until,'Bridge did not restart'
        time.sleep(.1)
    assert rpc('example-account','flow.get',params)['state']['status']=='completed'
    print('PASS extracted bridge/worker: independent credentials, explicit grants, account isolation, approval/callback replay, dependent completion, cancellation and restart')
except BaseException:
    for container in containers:
        result=subprocess.run(['docker','logs','--tail','30',container],capture_output=True,text=True)
        output=result.stdout+result.stderr
        for token in tokens.values():output=output.replace(token,'<REDACTED>')
        print(output,file=sys.stderr)
    raise
finally:
    for container in reversed(containers):subprocess.run(['docker','rm','-f',container],capture_output=True)
    subprocess.run(['docker','volume','rm',volume],capture_output=True)
