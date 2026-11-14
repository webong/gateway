"""Stop old writer -> offline CLI copy -> new endpoint -> current-progress rollback."""
import copy, hashlib, json, pathlib, secrets, subprocess, sys, tempfile, time, uuid
from urllib.request import Request,urlopen
from urllib.error import HTTPError,URLError

image=sys.argv[1];name='weaver-cutover-'+uuid.uuid4().hex[:10];volume=name+'-state'
containers=[];token=secrets.token_urlsafe(32);newtoken=secrets.token_urlsafe(32)
base=''
def docker(*args):
    r=subprocess.run(['docker',*args],capture_output=True,text=True)
    if r.returncode:raise AssertionError('docker '+args[0]+' failed: '+redact(r.stdout+r.stderr))
    return r.stdout.strip()
def redact(text):
    return text.replace(token,'<REDACTED>').replace(newtoken,'<REDACTED>')
def request(method=None,params=None,credential=None):
    data=None if method is None else json.dumps({'jsonrpc':'2.0','id':41,'method':method,'params':params}).encode()
    req=Request(base+('/healthz' if method is None else '/rpc'),data=data,headers={'Content-Type':'application/json','Authorization':'Bearer '+(credential or token)})
    with urlopen(req,timeout=5) as response:
        raw=response.read();return response.status,json.loads(raw) if raw else None

def rpc(method,params,credential=None):
    status,response=request(method,params,credential)
    assert status==200 and 'error' not in response, response
    return response['result']['state']

def start(suffix,legacy=False,rollback=False):
    global base
    container=name+'-'+suffix
    args=['run','-d','--name',container,'--user','10001:10001','--mount','type=volume,source='+volume+',target=/state','-p','127.0.0.1::8080']
    if legacy:
        args+=['-e','WORKFLOW_RPC_TOKEN='+token,'-e','WORKFLOW_RPC_LISTEN=0.0.0.0:8080','-e','WORKFLOW_PHP_PUBLIC=/app/app/public',
               '-e','WORKFLOW_BOOTSTRAP=/app/app/bootstrap.example.php','-e','WORKFLOW_RUNTIME_BOOTSTRAP=/extension/tests/fixtures/cutover-legacy-runtime.php',
               '-e','CUTOVER_DATABASE='+('rollback' if rollback else 'source'),'--entrypoint','/usr/local/bin/workflowd']
    else:
        args+=['-e','GATEWAY_WEAVER_CONTEXT=/state/runtime.json','-e','GATEWAY_WEAVER_RPC_LISTEN=0.0.0.0:8080',
               '-e','GATEWAY_WEAVER_PHP_PUBLIC=/extension/public','--entrypoint','/usr/local/bin/gateway-workflow-bridge']
    docker(*args,image);containers.append(container)
    base='http://127.0.0.1:'+docker('port',container,'8080/tcp').split(':')[-1]
    until=time.monotonic()+40
    while True:
        try:
            if request()[0]==204:break
        except (URLError,HTTPError,TimeoutError,ConnectionError):pass
        assert time.monotonic()<until,'endpoint readiness failed';time.sleep(.1)
    return container

def offline(mode):
    docker('run','--rm','--user','10001:10001','--mount','type=volume,source='+volume+',target=/state',
           '-e','GATEWAY_WEAVER_CUTOVER_CONTEXT=/state/'+mode+'.json','--entrypoint','php',image,'bin/cutover','--'+mode,'--writers-stopped')

try:
    configuration=json.loads((pathlib.Path(__file__).resolve().parents[1]/'examples/approval-order.configuration.json').read_text())
    configuration['worker_mode']='poll'
    account=configuration['accounts'][0]['id'];params={'flow_key':'order-approval','run_id':'before-cutover','subject':{'type':'order','id':'42'},'driver':'queue'}
    runtime={'configuration':configuration,'database':{'dsn':'sqlite:/state/weaver.sqlite'},'authentication':{account:newtoken}}
    copy_context={'source':{'dsn':'sqlite:/state/source.sqlite'},'destination':runtime['database'],'configuration':configuration,
                  'assignments':[{'account':account,'subject':params['subject']}]}
    rollback_context={'source':runtime['database'],'destination':{'dsn':'sqlite:/state/rollback.sqlite'}}
    docker('volume','create',volume);setup=name+'-setup'
    docker('create','--name',setup,'--mount','type=volume,source='+volume+',target=/state','--entrypoint','php',image,'tests/fixtures/cutover-setup.php');containers.append(setup)
    with tempfile.TemporaryDirectory(prefix=name) as directory:
        for filename,document in [('runtime',runtime),('copy',copy_context),('rollback',rollback_context)]:
            path=pathlib.Path(directory)/(filename+'.json');path.write_text(json.dumps(document));docker('cp',str(path),setup+':/state/'+path.name)
    docker('start','-a',setup);docker('rm',setup);containers.remove(setup)
    old=start('legacy',legacy=True);rpc('flow.start',params)
    docker('exec',old,'php','/app/app/queue-worker.php','--once')
    waiting=rpc('flow.get',params);assert waiting['steps']['approve']['metadata']['deferred']
    callback={**params,'step_id':'approve','attempt':waiting['steps']['approve']['attempts'],'idempotency_key':'issued-before-endpoint-switch','result':{'status':'completed'}}
    docker('stop','--time','10',old)  # Old writer stopped before copying or new endpoint.
    offline('copy')
    current=start('weaver');state=rpc('flow.get',params,newtoken)
    assert state==waiting,'copied public state changed'
    try:request('flow.get',params,token);raise AssertionError('old credential accepted by Weaver')
    except HTTPError as error:assert error.code==401
    rpc('flow.complete',callback,newtoken);rpc('flow.complete',callback,newtoken)
    docker('exec','-e','GATEWAY_WEAVER_CONTEXT=/state/runtime.json',current,'php','bin/worker','--once')
    completed=rpc('flow.get',params,newtoken);assert completed['status']=='completed'
    docker('stop','--time','10',current)
    offline('rollback')
    restored=start('restored',legacy=True,rollback=True)
    assert rpc('flow.get',params)==completed,'rollback lost current queue progress'
    rpc('flow.complete',callback);assert rpc('flow.get',params)==completed,'callback replay changed rolled-back run'
    print('PASS copied-data endpoint cutover/rollback: stopped writers, original execution IDs and pinned state, old callback under new credentials, current-progress legacy rollback, replay deduplication',flush=True)
except BaseException:
    for container in containers:
        r=subprocess.run(['docker','logs','--tail','12',container],capture_output=True,text=True);print(redact(r.stdout+r.stderr),file=sys.stderr)
    raise
finally:
    for container in reversed(containers):subprocess.run(['docker','rm','-f',container],capture_output=True)
    subprocess.run(['docker','volume','rm',volume],capture_output=True)
