"""Fresh Composer HTTPS download against private Docker fixtures; publishes nothing."""
import argparse,json,pathlib,shutil,subprocess,sys,tempfile,time,uuid

parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('image');parser.add_argument('--consumer',default='gateway-weaver-planner-test')
args=parser.parse_args();root=pathlib.Path(__file__).resolve().parents[3]
name='weaver-https-composer-'+uuid.uuid4().hex[:8];containers=[];network=False
def docker(*values,check=True):
    result=subprocess.run(['docker',*map(str,values)],text=True,capture_output=True)
    if check and result.returncode:raise RuntimeError(result.stdout+result.stderr)
    return result
try:
    with tempfile.TemporaryDirectory(prefix=name) as directory:
        release=pathlib.Path(directory);release.chmod(0o755)
        subprocess.run([sys.executable,str(root/'pkg/weaver/package.py'),args.image,str(release),
            '--asset-base','https://weaver-artifacts:8443'],check=True,capture_output=True)
        reports=list(release.glob('weaver-*.json'));assert len(reports)==1
        arch=json.loads(reports[0].read_text())['platform']['arch']
        shutil.copyfile(release/('packages-'+arch+'.json'),release/'packages.json')
        subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','1',
            '-keyout',str(release/'key.pem'),'-out',str(release/'ca.pem'),'-subj','/CN=weaver-artifacts',
            '-addext','subjectAltName=DNS:weaver-artifacts'],check=True,capture_output=True)
        (release/'fixture.ini').write_text('curl.cainfo=/release/ca.pem\nopenssl.cafile=/release/ca.pem\n')
        docker('network','create',name);network=True
        server=name+'-server'
        # Server and consumers share only a private Docker network; no host ports.
        docker('run','-d','--name',server,'--network',name,'--network-alias','weaver-artifacts',
            '--mount','type=bind,source='+str(root/'tools/workflow-runtime-gate')+',target=/gate,readonly',
            '--mount','type=bind,source='+str(release)+',target=/release,readonly',
            '--entrypoint','php',args.consumer,'/gate/artifact-server.php','/release');containers.append(server)
        until=time.monotonic()+30
        while docker('exec',server,'php','-d','openssl.cafile=/release/ca.pem','-r',
            "exit(@file_get_contents('https://weaver-artifacts:8443/healthz')==='OK'?0:1);",check=False).returncode:
            if time.monotonic()>until:raise RuntimeError('Private HTTPS fixture did not start')
            time.sleep(.25)
        probe=docker('run','--rm','--network',name,'--entrypoint','php',args.consumer,'-r',
            "exit(@file_get_contents('https://weaver-artifacts:8443/packages.json')===false?0:1);",check=False)
        assert probe.returncode==0,'Default TLS trust unexpectedly accepted the fixture CA'
        def consume():
            return docker('run','--rm','--network',name,
                '--mount','type=bind,source='+str(release)+',target=/release,readonly',
                '--mount','type=bind,source='+str(root/'pkg/weaver/tests/composer-dist-smoke.php')+',target=/composer-dist-smoke.php,readonly',
                '-e','PHP_INI_SCAN_DIR=:/release','-e','COMPOSER_CAFILE=/release/ca.pem',
                '-e','COMPOSER_CACHE_DIR=/tmp/weaver-empty-composer-cache',
                '-e','WEAVER_COMPOSER_REPOSITORY=https://weaver-artifacts:8443',
                '--entrypoint','php',args.consumer,'/composer-dist-smoke.php',arch,check=False)
        archive=release/('gateway-weaver-composer-'+arch+'.tar.gz');original=archive.read_bytes()
        archive.write_bytes(original+b'corrupt-fixture')
        rejected=consume();output=rejected.stdout+rejected.stderr
        assert rejected.returncode!=0 and 'checksum' in output.lower(),'Corrupt Composer dist did not fail checksum verification: '+output[-3000:]
        archive.write_bytes(original)
        accepted=consume()
        assert accepted.returncode==0,accepted.stdout+accepted.stderr
        assert 'PASS clean Planner consumer' in accepted.stdout
        accesses=docker('logs',server).stdout
        assert 'Composer/' in accesses and 'gateway-weaver-composer-'+arch+'.tar.gz' in accesses,'Composer never downloaded the source archive over HTTPS'
        print('PASS fresh Composer HTTPS index/dist, explicit CA trust, untrusted-CA rejection, corrupt-dist rejection, empty download cache and installed Planner/CLI acceptance')
finally:
    for container in reversed(containers):docker('rm','-f',container,check=False)
    if network:docker('network','rm',name,check=False)
