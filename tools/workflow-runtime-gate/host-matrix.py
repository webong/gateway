"""Actual installed-package acceptance across the production Spinner/Router entrypoints.
Uses disposable containers, volumes and Planner state. HTTPS artifact download is
seeded through verified caches, as in Gateway's existing container smoke tests.
"""
import hashlib, json, os, pathlib, shutil, subprocess, sys, tarfile, tempfile, time, uuid
from urllib.request import Request, urlopen
from urllib.error import HTTPError, URLError

mode=sys.argv[1]
print(mode+': preparing verified package and disposable host',flush=True)
assert mode in ('spinner','roadrunner','frankenphp')
image=sys.argv[2] if len(sys.argv)>2 else 'gateway-workflow-runtime-gate:arm64'
root=pathlib.Path(__file__).resolve().parents[2]
name='gw-workflow-host-'+mode+'-'+uuid.uuid4().hex[:8]
network=name+'-net';host=name;planner=name+'-planner' if mode=='spinner' else host
internal='disposable-host-matrix-internal-token'
registry='disposable-host-matrix-registry-token'
guest='disposable-runtime-gate-token'
containers=[];volumes=[]

def docker(*args,check=True):
    r=subprocess.run(['docker',*map(str,args)],text=True,capture_output=True)
    if check and r.returncode:raise AssertionError('docker '+str(args[0])+' failed: '+redact(r.stdout+r.stderr))
    return r

def redact(value):
    for token in (internal,registry,guest):value=value.replace(token,'<REDACTED>')
    return value

def wait(predicate,message,timeout=75):
    until=time.monotonic()+timeout
    while time.monotonic()<until:
        if predicate():return
        time.sleep(.25)
    raise AssertionError(message)

def http(port,path,auth=None,extension=None,method="GET"):
    headers={}
    if auth:headers['Authorization']='Bearer '+auth
    if extension:headers['X-Gateway-Extension-Authorization']='Bearer '+extension
    req=Request('http://127.0.0.1:'+str(port)+path,headers=headers,method=method,data=b'{}' if method=='POST' else None)
    try:
        with urlopen(req,timeout=2) as r:return r.status,r.read()
    except HTTPError as e:return e.code,e.read()
    except (URLError,TimeoutError,ConnectionError):return 0,b''

def mapped(container,port):
    return docker('port',container,str(port)+'/tcp').stdout.strip().split(':')[-1]

def process_count():
    top=docker('top',host,'-eo','pid,args',check=False).stdout
    return sum('/gate/worker.php' in line or '/runtime/bin/workflowd' in line for line in top.splitlines())

def fixture(phase):
    docker('exec','--user','www-data',planner,'php','/gate/host-activation.php',phase)

def php(phase):
    result=docker('exec','-e','PHP_INI_SCAN_DIR='+installed+'/runtime/php.d',
      '-e','LD_LIBRARY_PATH='+installed+'/runtime/lib','-e','WORKFLOW_GATE_HOST_TOKEN='+internal,
      host,installed+'/runtime/loader','--library-path',installed+'/runtime/lib',installed+'/runtime/bin/php',
      '-c',installed+'/php.ini','-d','extension_dir='+installed+'/runtime/lib',installed+'/gate/http-smoke.php',
      'http://127.0.0.1:5052',phase)
    print(mode+': '+result.stdout.strip())

def volume(suffix,host_image):
    v=name+'-'+suffix;docker('volume','create',v);volumes.append(v)
    docker('run','--rm','--user','0','--mount','type=volume,source='+v+',target=/fixture',
           '--entrypoint','sh',host_image,'-c','chmod 0777 /fixture')
    return v

try:
  with tempfile.TemporaryDirectory(prefix='gateway-workflow-host-') as tmp:
    tmp=pathlib.Path(tmp);package=tmp/'webong%2fgateway-workflow'
    export=name+'-export';docker('create','--name',export,image);containers.append(export)
    docker('cp',export+':/package',package);docker('rm',export);containers.remove(export)
    manifest=json.loads((package/'plugin.json').read_text())
    assert manifest['descriptor']['identity']['id']=='webong/gateway-workflow'
    archive=tmp/'workflow.tar.gz'
    with tarfile.open(archive,'w:gz',format=tarfile.USTAR_FORMAT) as tar:tar.add(package,arcname=package.name)
    sha=hashlib.sha256(archive.read_bytes()).hexdigest()
    spinner_cache=tmp/'spinner-cache';planner_cache=tmp/'planner-cache'
    for cache in (spinner_cache,planner_cache):
        cache.mkdir();(cache/'.desired').mkdir();shutil.copytree(package,cache/package.name)
        for directory,dirs,files in os.walk(cache):os.chmod(directory,0o755)
        os.chmod(cache,0o777);os.chmod(cache/'.desired',0o777)
    receipt=hashlib.sha256(b'webong/gateway-workflow').hexdigest()+'.json'
    (spinner_cache/'.desired'/receipt).write_text(json.dumps({'extension':'webong/gateway-workflow','version':'0.0.0-gate','revision':1,'artifact_sha256':sha,'package_revision':manifest['descriptor']['identity']['revision']}))
    (planner_cache/'.desired'/receipt).write_text(json.dumps({'revision':1,'sha256':sha}))
    os.chmod(spinner_cache/'.desired'/receipt,0o666)
    installed='/extensions/'+package.name
    host_image='gateway-workflow-host:'+mode
    planner_image='gateway-workflow-host:roadrunner'
    docker('network','create',network)
    state=volume('state',host_image);data=volume('data',host_image)
    common=['-e','APP_KEY=base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=',
      '-e','GATEWAY_INTERNAL_TOKEN='+internal,'-e','REGISTRY_TOKEN='+registry,
      '-e','GATEWAY_ROUTER_DATA_DIR=/data','-e','GATEWAY_ROUTER_CREDENTIALS_FILE=/gate/host.env','-e','DB_CONNECTION=sqlite','-e','DB_DATABASE=/data/gateway.sqlite',
      '-e','GATEWAY_PLUGIN_DIR=/extensions','-e','GATEWAY_COMPANION_PLUGIN_DIR=/planner-extensions',
      '-e','WORKFLOW_GATE_ARTIFACT_SHA='+sha,
      '--mount','type=bind,source='+str(root/'tools/workflow-runtime-gate')+',target=/gate,readonly',
      '--mount','type=bind,source='+str(planner_cache)+',target=/planner-extensions']
    if mode=='spinner':
      docker('run','-d','--name',planner,'--network',network,'--network-alias','planner','--user','www-data',
        *common,'--mount','type=volume,source='+data+',target=/data','-p','127.0.0.1::8080',
        '--entrypoint','sh',planner_image,'/gate/host-planner.sh');containers.append(planner)
      management=mapped(planner,8080)
      wait(lambda:http(management,'/health')[0] in (200,204),'separate Planner did not become ready')
      fixture('activate')
      docker('run','-d','--name',host,'--network',network,*common,
        '--mount','type=bind,source='+str(spinner_cache)+',target=/extensions',
        '--mount','type=volume,source='+state+',target=/state',
        '-e','GATEWAY_RUNTIME=http','-e','GATEWAY_PLANNER_URL=http://planner:8080',
        '-e','GATEWAY_DELIVERY_SPOOL_PATH=/tmp/spool','-e','GATEWAY_DELIVERY_ADMIN_ADDR=:5052',
        '-e','PORT=5001','-e','LOG_LEVEL=error','-p','127.0.0.1::5001','-p','127.0.0.1::5052',host_image)
      containers.append(host);public=mapped(host,5001)
    else:
      docker('run','-d','--name',host,'--network',network,*common,
        '--mount','type=bind,source='+str(spinner_cache)+',target=/extensions',
        '--mount','type=volume,source='+state+',target=/state','--mount','type=volume,source='+data+',target=/data',
        '-e','GATEWAY_ROUTER_LISTEN=0.0.0.0:8080','-e','GATEWAY_ROUTER_TLS_LISTEN=',
        '-e','GATEWAY_DELIVERY_ADMIN_ADDR=0.0.0.0:5052','-e','GATEWAY_PLANNER_WORKERS=1',
        '-p','127.0.0.1::8080','-p','127.0.0.1::5052',host_image)
      containers.append(host);public=mapped(host,8080);management=public
      wait(lambda:http(public,'/health')[0] in (200,204),'Router did not become ready')
      fixture('activate')
    admin=mapped(host,5052)
    wait(lambda:process_count()==2 and http(admin,'/workflow/rpc',internal,guest)[0]==405,'verified package did not activate through actual host')
    assert http(admin,'/workflow/rpc',guest,method='POST')[0]==401,'extension credential bypassed host authentication'
    assert http(admin,'/workflow/rpc',internal,method='POST')[0]==401,'host token bypassed extension authentication'
    assert http(admin,'/workflow/rpc',internal,'wrong',method='POST')[0]==401,'wrong extension token accepted'
    assert http(public,'/workflow/rpc',internal,guest)[0] not in (200,405),'private RPC leaked onto public edge'
    print(mode+': installed package activated; credential boundary passed',flush=True)
    php('start')
    docker('restart','--timeout','10',host)
    public=mapped(host,5001 if mode=='spinner' else 8080);admin=mapped(host,5052)
    wait(lambda:process_count()==2 and http(admin,'/workflow/rpc',internal,guest)[0]==405,'runtime did not recover after host restart')
    php('restart');php('abandon')
    docker('kill',host);docker('start',host)
    admin=mapped(host,5052)
    wait(lambda:process_count()==2 and http(admin,'/workflow/rpc',internal,guest)[0]==405,'runtime did not recover after host crash')
    php('lease-recovered')
    php('abandon-nonidempotent')
    docker('kill',host);docker('start',host)
    admin=mapped(host,5052)
    wait(lambda:process_count()==2 and http(admin,'/workflow/rpc',internal,guest)[0]==405,'runtime did not recover for non-idempotent reconciliation')
    php('nonidempotent-fenced')
    docker('exec','--workdir',installed,host,installed+'/bin/spinner','--kill-runtime-guest')
    wait(lambda:process_count()==0,'abruptly terminated guest left live PHP children',timeout=15)
    fixture('activate')
    wait(lambda:process_count()==2 and http(admin,'/workflow/rpc',internal,guest)[0]==405,'runtime did not recover after guest termination')
    php('restart')
    fixture('disable')
    wait(lambda:process_count()==0 and http(admin,'/workflow/rpc',internal,guest)[0]==503,'disabled extension retained listeners or child processes')
    fixture('activate')
    wait(lambda:process_count()==2 and http(admin,'/workflow/rpc',internal,guest)[0]==405,'extension failed to reactivate')
    fixture('remove-secret')
    wait(lambda:process_count()==0 and http(admin,'/workflow/rpc',internal,guest)[0] in (502,503),'lost secret retained active children')
    print(mode+': PASS actual host activation, independent credentials, private-only routing, queue RPC/restart/lease recovery, disable and secret-loss teardown; artifact '+sha)
except BaseException:
  for container in containers:
    if container.endswith('-export'):continue
    print(redact(docker('logs','--tail','45',container,check=False).stdout+docker('logs','--tail','45',container,check=False).stderr),file=sys.stderr)
  raise
finally:
  for container in reversed(containers):docker('rm','-f',container,check=False)
  for v in volumes:docker('volume','rm',v,check=False)
  docker('network','rm',network,check=False)
