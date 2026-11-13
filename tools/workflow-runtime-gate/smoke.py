"""Run a disposable runtime gate. No source app or production database is modified."""
import json, subprocess, sys, time, uuid
image = sys.argv[1] if len(sys.argv)>1 else 'gateway-workflow-runtime-gate:arm64'
name = 'gateway-workflow-gate-'+uuid.uuid4().hex[:10]
volume = name+'-state'
def docker(*args, check=True):
    result=subprocess.run(['docker',*args],check=False,text=True,capture_output=True)
    if check and result.returncode!=0: raise AssertionError('docker '+args[0]+' failed: '+result.stdout+result.stderr)
    return result
def boot():
    docker('run','-d','--name',name,'--mount','type=volume,source='+volume+',target=/state',image,'--session-probe')
    until=time.monotonic()+30
    while time.monotonic()<until:
        for line in docker('logs',name).stdout.splitlines():
            if line.startswith('{'):
                result=json.loads(line)
                assert not result.get('http'), 'Public HTTP advertised'
                mounts=result['privateHttp'];assert len(mounts)==1 and mounts[0]['prefix']=='/workflow/rpc'
                return mounts[0]['address']
        status=docker('inspect',name,'--format','{{.State.Status}}').stdout.strip()
        if status=='exited':raise AssertionError(docker('logs',name).stderr)
        time.sleep(.1)
    raise AssertionError('runtime did not become ready')
def php(base, phase):
    result=docker('exec','-e','PHP_INI_SCAN_DIR=/package/runtime/php.d','-e','LD_LIBRARY_PATH=/package/runtime/lib',name,'/package/runtime/loader','--library-path','/package/runtime/lib','/package/runtime/bin/php','-c','/package/php.ini','/package/gate/http-smoke.php',base,phase)
    print(result.stdout.strip())
def stop():
    docker('stop','--time','10',name)
    assert docker('inspect',name,'--format','{{.State.ExitCode}}').stdout.strip()=='0','Shutdown did not drain cleanly'
    docker('rm',name)
try:
    docker('volume','create',volume)
    base=boot();php(base,'start');stop()
    base=boot();php(base,'restart');php(base,'abandon')
    docker('kill',name);docker('rm',name);time.sleep(1.3)
    base=boot();php(base,'lease-recovered');php(base,'abandon-nonidempotent')
    docker('kill',name);docker('rm',name);time.sleep(1.3)
    base=boot();php(base,'nonidempotent-fenced');stop()
    denied=docker('run','--rm',image,'--session-no-secret',check=False)
    assert denied.returncode!=0 and 'privateHttp' not in denied.stdout, 'Missing scoped secret did not fail activation'
    print('PASS: missing scoped secret fails closed')
    docker('create','--name',name,image,'--session-probe')
    import tempfile, os
    with tempfile.NamedTemporaryFile() as corrupted:
        corrupted.write(b'not a PHP executable');corrupted.flush()
        docker('cp',corrupted.name,name+':/package/runtime/bin/php')
    invalid=docker('start','-a',name,check=False)
    assert invalid.returncode!=0 and 'privateHttp' not in invalid.stdout and 'artifact verification failed' in invalid.stderr, 'Corrupted runtime was activated'
    docker('rm',name)
    print('PASS: changed/missing runtime rejected before activation')
    print('PASS: scratch runtime/PDO readiness, private advertisement and bounded clean shutdown')
finally:
    docker('rm','-f',name,check=False)
    docker('volume','rm',volume,check=False)
