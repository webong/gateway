"""Actual installed-package acceptance across the production Spinner/Router entrypoints.
Uses disposable containers, volumes and Planner state. HTTPS artifact download is
seeded through verified caches, as in Gateway's existing container smoke tests.
"""
import gzip, hashlib, json, os, pathlib, shutil, subprocess, sys, tarfile, tempfile, time, uuid
from urllib.request import Request, urlopen
from urllib.error import HTTPError, URLError

mode=sys.argv[1]
print(mode+': preparing verified package and disposable host',flush=True)
assert mode in ('spinner','roadrunner','frankenphp')
image=sys.argv[2] if len(sys.argv)>2 else 'gateway-workflow-runtime-gate:arm64'
root=pathlib.Path(__file__).resolve().parents[2]
name='gw-workflow-host-'+mode+'-'+uuid.uuid4().hex[:8]
network=name+'-net';provider_network=name+'-effects';host=name;planner=name+'-planner' if mode=='spinner' else host
internal='disposable-host-matrix-internal-token'
registry='disposable-host-matrix-registry-token'
guest='disposable-weaver-runtime-gate-token'
provider_token='disposable-weaver-provider-token'
containers=[];volumes=[]

def docker(*args,check=True):
    r=subprocess.run(['docker',*map(str,args)],text=True,capture_output=True)
    if check and r.returncode:raise AssertionError('docker '+str(args[0])+' failed: '+redact(r.stdout+r.stderr))
    return r

def redact(value):
    for token in (internal,registry,guest,'disposable-weaver-runtime-gate-token','disposable-weaver-runtime-gate-token-rotated','disposable-weaver-provider-token','disposable-weaver-provider-token-rotated','disposable-weaver-provider-other-account'):value=value.replace(token,'<REDACTED>')
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
    if method=='POST':headers['Content-Type']='application/json'
    req=Request('http://127.0.0.1:'+str(port)+path,headers=headers,method=method,data=b'{}' if method=='POST' else None)
    try:
        with urlopen(req,timeout=2) as r:return r.status,r.read()
    except HTTPError as e:return e.code,e.read()
    except (URLError,TimeoutError,ConnectionError):return 0,b''

def mapped(container,port):
    return docker('port',container,str(port)+'/tcp').stdout.strip().split(':')[-1]

def process_count():
    top=docker('top',host,'-eo','pid,args',check=False).stdout
    return sum('/gate/worker.php' in line or '/bin/worker' in line or '/runtime/bin/workflowd' in line for line in top.splitlines())

def fixture(phase):
    script='/gate/host-weaver-activation.php' if production else '/gate/host-activation.php'
    docker('exec','--user','www-data','-e','WORKFLOW_GATE_ACCOUNT_TOKEN='+guest,'-e','WORKFLOW_GATE_PROVIDER_TOKEN='+provider_token,planner,'php',script,phase)

def php(phase,script=None):
    if script is None:script='/gate/weaver-http-smoke.php' if production else installed+'/gate/http-smoke.php'
    result=docker('exec','-e','PHP_INI_SCAN_DIR='+installed+'/runtime/php.d',
      '-e','LD_LIBRARY_PATH='+installed+'/runtime/lib','-e','WORKFLOW_GATE_HOST_TOKEN='+internal,
      '-e','WEAVER_PACKAGE_ROOT='+installed,'-e','WORKFLOW_GATE_ACCOUNT_TOKEN='+guest,
      host,installed+'/runtime/loader','--library-path',installed+'/runtime/lib',installed+'/runtime/bin/php',
      '-c',installed+'/php.ini','-d','extension_dir='+installed+'/runtime/lib',script,
      'http://127.0.0.1:5052',phase)
    print(mode+': '+result.stdout.strip(),flush=True)

def wait_php(phase):
    until=time.monotonic()+75
    while True:
        try:
            php(phase);return
        except AssertionError:
            if time.monotonic()>=until:raise
            time.sleep(.25)

def volume(suffix,host_image):
    v=name+'-'+suffix;docker('volume','create',v);volumes.append(v)
    docker('run','--rm','--user','0','--mount','type=volume,source='+v+',target=/fixture',
           '--entrypoint','sh',host_image,'-c','chmod 0777 /fixture')
    return v

try:
  with tempfile.TemporaryDirectory(prefix='gateway-workflow-host-') as tmp:
    tmp=pathlib.Path(tmp);package=tmp/'webong%2fgateway-weaver'
    export=name+'-export';docker('create','--name',export,image);containers.append(export)
    docker('cp',export+':/package',package);docker('rm',export);containers.remove(export)
    manifest=json.loads((package/'plugin.json').read_text())
    assert manifest['descriptor']['identity']['id']=='webong/gateway-weaver'
    production='gateway.weaver/v1' in manifest.get('metadata',{})
    version=manifest['descriptor']['identity']['version']
    archive=tmp/'workflow.tar.gz'
    def normalized(info):
        info.uid=info.gid=0;info.uname=info.gname='';info.mtime=0
        return info
    with archive.open('wb') as output:
      with gzip.GzipFile(filename='',mode='wb',fileobj=output,mtime=0) as compressed:
        with tarfile.open(fileobj=compressed,mode='w',format=tarfile.USTAR_FORMAT) as tar:
          tar.add(package,arcname=package.name,filter=normalized)
    sha=hashlib.sha256(archive.read_bytes()).hexdigest()
    spinner_cache=tmp/'spinner-cache';planner_cache=tmp/'planner-cache'
    for cache in (spinner_cache,planner_cache):
        cache.mkdir();(cache/'.desired').mkdir();shutil.copytree(package,cache/package.name)
        for directory,dirs,files in os.walk(cache):os.chmod(directory,0o755)
        os.chmod(cache,0o777);os.chmod(cache/'.desired',0o777)
    receipt=hashlib.sha256(b'webong/gateway-weaver').hexdigest()+'.json'
    (spinner_cache/'.desired'/receipt).write_text(json.dumps({'extension':'webong/gateway-weaver','version':version,'revision':1,'artifact_sha256':sha,'package_revision':manifest['descriptor']['identity']['revision']}))
    (planner_cache/'.desired'/receipt).write_text(json.dumps({'revision':1,'sha256':sha}))
    os.chmod(spinner_cache/'.desired'/receipt,0o666)
    installed='/extensions/'+package.name
    host_image='gateway-workflow-host:'+mode
    planner_image='gateway-workflow-host:roadrunner'
    docker('network','create',network)
    state=volume('state',host_image);data=volume('data',host_image)
    if production:
      docker('run','--rm','--mount','type=volume,source='+state+',target=/state',
        '-e','PHP_INI_SCAN_DIR=/package/runtime/php.d','-e','LD_LIBRARY_PATH=/package/runtime/lib',
        '--entrypoint','/package/runtime/loader',image,'--library-path','/package/runtime/lib',
        '/package/runtime/bin/php','-c','/package/php.ini','-r',
        r"require '/package/vendor/autoload.php'; \Webong\Gateway\Weaver\OperationalSchema::install(new PDO('sqlite:/state/workflow.sqlite')); chmod('/state/workflow.sqlite',0666);")
    if production:
      # An isolated fixture network uses public-class addresses so the actual
      # production SSRF policy stays enabled. --internal forbids external routes.
      address=uuid.uuid4().bytes
      docker('network','create','--internal','--subnet','11.'+str(address[0])+'.'+str(address[1])+'.0/24',provider_network)
      provider=name+'-provider'
      docker('run','-d','--name',provider,'--network',provider_network,'--network-alias','weaver-provider','--mount','type=volume,source='+state+',target=/state',
        '--mount','type=bind,source='+str(root/'tools/workflow-runtime-gate')+',target=/gate,readonly',
        '-e','PHP_INI_SCAN_DIR=/package/runtime/php.d','--entrypoint','/package/runtime/loader',image,'--library-path','/package/runtime/lib',
        '/package/runtime/bin/php','-c','/package/php.ini','-S','0.0.0.0:8089','/gate/http-provider.php');containers.append(provider)
      wait(lambda:docker('exec',provider,'/package/runtime/loader','--library-path','/package/runtime/lib','/package/runtime/bin/php','-c','/package/php.ini','-r',"exit(@file_get_contents('http://127.0.0.1:8089/healthz')===false?1:0);",check=False).returncode==0,'disposable HTTP provider did not become ready')
    common=['-e','APP_KEY=base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=',
      '-e','GATEWAY_INTERNAL_TOKEN='+internal,'-e','REGISTRY_TOKEN='+registry,
      '-e','GATEWAY_ROUTER_DATA_DIR=/data','-e','GATEWAY_ROUTER_CREDENTIALS_FILE=/gate/host.env','-e','DB_CONNECTION=sqlite','-e','DB_DATABASE=/data/gateway.sqlite',
      '-e','GATEWAY_PLUGIN_DIR=/extensions','-e','GATEWAY_COMPANION_PLUGIN_DIR=/planner-extensions',
      '-e','WORKFLOW_GATE_ARTIFACT_SHA='+sha,'-e','WORKFLOW_GATE_EXTENSION_VERSION='+version,
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
      containers.append(host)
      if production:docker('network','connect',provider_network,host)
      public=mapped(host,5001)
    else:
      docker('run','-d','--name',host,'--network',network,*common,
        '--mount','type=bind,source='+str(spinner_cache)+',target=/extensions',
        '--mount','type=volume,source='+state+',target=/state','--mount','type=volume,source='+data+',target=/data',
        '-e','GATEWAY_ROUTER_LISTEN=0.0.0.0:8080','-e','GATEWAY_ROUTER_TLS_LISTEN=',
        '-e','GATEWAY_DELIVERY_ADMIN_ADDR=0.0.0.0:5052','-e','GATEWAY_PLANNER_WORKERS=1',
        '-p','127.0.0.1::8080','-p','127.0.0.1::5052',host_image)
      containers.append(host)
      if production:docker('network','connect',provider_network,host)
      public=mapped(host,8080);management=public
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
    if production:
      php('http-effect');php('http-pending-rotation')
      fixture('changed-http-scope')
      wait(lambda:process_count()==0 and http(admin,'/workflow/rpc',internal,guest)[0] in (502,503),'changed provider replay scope retained active workers')
      provider_token='disposable-weaver-provider-token-rotated'
      fixture('activate')
      wait(lambda:process_count()==2 and http(admin,'/workflow/rpc',internal,guest)[0]==405,'same-scope provider credential rotation failed reactivation')
      php('http-after-rotation')
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
    if production:php('kill','/gate/kill-weaver.php')
    else:docker('exec','--workdir',installed,host,installed+'/bin/spinner','--kill-runtime-guest')
    wait(lambda:process_count()==0,'abruptly terminated guest left live PHP children',timeout=15)
    fixture('activate')
    wait(lambda:process_count()==2 and http(admin,'/workflow/rpc',internal,guest)[0]==405,'runtime did not recover after guest termination')
    php('restart')
    if production:
      fixture('rotate-secret')
      wait(lambda:process_count()==0 and http(admin,'/workflow/rpc',internal,guest)[0] in (502,503),'credential rotation retained active children')
      previous=guest;guest='disposable-weaver-runtime-gate-token-rotated'
      fixture('activate')
      wait(lambda:process_count()==2 and http(admin,'/workflow/rpc',internal,guest)[0]==405,'rotated credential failed reactivation')
      assert http(admin,'/workflow/rpc',internal,previous,method='POST')[0]==401,'previous credential accepted after rotation'
      php('restart')
      fixture('revoke-grants')
      # A projected resource change must replace the runtime without an activation bump.
      wait(lambda:http(admin,'/workflow/rpc',internal,guest,method='POST')[0]==200,'projected grant change did not restore RPC')
      until=time.monotonic()+75
      while True:
        result=docker('exec','-e','WORKFLOW_GATE_HOST_TOKEN='+internal,'-e','WORKFLOW_GATE_ACCOUNT_TOKEN='+guest,
          '-e','WEAVER_PACKAGE_ROOT='+installed,'-e','PHP_INI_SCAN_DIR='+installed+'/runtime/php.d',host,
          installed+'/runtime/loader','--library-path',installed+'/runtime/lib',installed+'/runtime/bin/php','-c',installed+'/php.ini',
          '-d','extension_dir='+installed+'/runtime/lib','/gate/weaver-http-smoke.php','http://127.0.0.1:5052','grant-denied',check=False)
        if result.returncode==0:break
        assert time.monotonic()<until,'live grant revocation did not deny existing run'
        time.sleep(.25)
      fixture('activate')
      wait(lambda:process_count()==2 and http(admin,'/workflow/rpc',internal,guest)[0]==405,'grant restoration failed')
      wait_php('restart')
      fixture('invalid-config')
      wait(lambda:process_count()==0 and http(admin,'/workflow/rpc',internal,guest)[0] in (502,503),'invalid projected configuration retained active children')
      fixture('activate')
      wait(lambda:process_count()==2 and http(admin,'/workflow/rpc',internal,guest)[0]==405,'valid configuration failed reactivation')
      fixture('unavailable-storage')
      wait(lambda:process_count()==0 and http(admin,'/workflow/rpc',internal,guest)[0] in (502,503),'unavailable operational storage retained active children')
      fixture('activate')
      wait(lambda:process_count()==2 and http(admin,'/workflow/rpc',internal,guest)[0]==405,'operational storage restoration failed')
      php('restart')
      print(mode+': PASS credential rotation, live grant revocation, invalid configuration and unavailable storage',flush=True)
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
  docker('network','rm',provider_network,check=False)
  docker('network','rm',network,check=False)
