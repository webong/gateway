"""Disposable stopped-writer operator acceptance through the original app and real host."""
import json,os,pathlib,tempfile,time
from urllib.request import Request,urlopen
from urllib.error import HTTPError,URLError

LEGACY_TOKEN='disposable-operator-legacy-token'
PARAMS={'flow_key':'operator_order','run_id':'issued-before-cutover','subject':{'type':'order','id':'operator-subject'},'driver':'queue'}

class OperatorCutover:
    def __init__(self,docker,wait,mapped,root,package,image,legacy_image,state,network,name,containers,host_image,mode):
        self.docker=docker;self.wait=wait;self.mapped=mapped;self.root=root;self.package=package;self.image=image
        self.legacy_image=legacy_image;self.state=state;self.network=network;self.name=name;self.containers=containers
        self.host_image=host_image;self.mode=mode
        self.mounts=['--mount','type=volume,source='+state+',target=/state',
            '--mount','type=bind,source='+str(package)+',target=/extension,readonly',
            '--mount','type=bind,source='+str(root/'tools/workflow-runtime-gate')+',target=/gate,readonly']
        self.env=['-e','WORKFLOW_RUNTIME_BOOTSTRAP=/gate/operator-legacy-runtime.php']

    def helper(self,action,database='legacy',extra=(),check=True):
        return self.docker('run','--rm',*self.mounts,*self.env,'-e','OPERATOR_DATABASE='+database,*extra,
            '--entrypoint','php',self.legacy_image,'/gate/operator-state.php',action,check=check)

    def rpc(self,url,method,params,token=LEGACY_TOKEN,host_token=None):
        headers={'Content-Type':'application/json','Authorization':'Bearer '+(host_token or token)}
        if host_token:headers['X-Gateway-Extension-Authorization']='Bearer '+token
        request=Request(url,data=json.dumps({'jsonrpc':'2.0','id':61,'method':method,'params':params}).encode(),headers=headers)
        with urlopen(request,timeout=5) as response:result=json.load(response)
        assert result.get('id')==61 and 'error' not in result,'Operator RPC was rejected'
        return result['result']['state']

    def start_legacy(self,suffix,database):
        container=self.name+'-operator-'+suffix
        self.docker('run','-d','--name',container,'--user','10001:10001','--network',self.network,*self.mounts,*self.env,
            '-e','OPERATOR_DATABASE='+database,'-e','WORKFLOW_RPC_TOKEN='+LEGACY_TOKEN,
            '-e','WORKFLOW_RPC_LISTEN=0.0.0.0:8080','-e','WORKFLOW_PHP_PUBLIC=/app/app/public',
            '-e','WORKFLOW_BOOTSTRAP=/app/app/bootstrap.example.php','-p','127.0.0.1::8080',
            '--entrypoint','/usr/local/bin/workflowd',self.legacy_image)
        self.containers.append(container)
        base='http://127.0.0.1:'+self.mapped(container,8080)
        def ready():
            try:
                with urlopen(base+'/healthz',timeout=2) as response:return response.status==204
            except (HTTPError,URLError,TimeoutError,ConnectionError):return False
        self.wait(ready,'original operator endpoint not ready')
        return container,base+'/rpc'

    def stop(self,container):
        self.docker('stop','--time','10',container)
        assert self.docker('inspect','--format','{{.State.Running}}',container).stdout.strip()=='false','writer did not stop'

    def document(self,container,name,value):
        with tempfile.TemporaryDirectory(prefix='operator-private-') as directory:
            path=pathlib.Path(directory)/name;path.write_text(json.dumps(value));path.chmod(0o644)
            self.docker('cp',path,container+':/state/'+name)

    def offline(self,mode,check=True):
        return self.docker('run','--rm','--mount','type=volume,source='+self.state+',target=/state',
            '-e','PHP_INI_SCAN_DIR=/package/runtime/php.d','-e','LD_LIBRARY_PATH=/package/runtime/lib',
            '-e','GATEWAY_WEAVER_CUTOVER_CONTEXT=/state/operator-'+mode+'.json',
            '--entrypoint','/package/runtime/loader',self.image,'--library-path','/package/runtime/lib',
            '/package/runtime/bin/php','-c','/package/php.ini','-d','extension_dir=/package/runtime/lib',
            '/package/bin/cutover','--'+mode,'--writers-stopped',check=check)

    def prepare(self):
        label=self.docker('image','inspect',self.legacy_image,'--format','{{index .Config.Labels "gateway.workflow.source"}}').stdout.strip()
        assert label=='00e0bbcdd61c2c6ed96af55865d0ef2e77557adb','legacy fixture source is not pinned'
        self.helper('prepare')
        self.original,url=self.start_legacy('original','legacy')
        self.rpc(url,'flow.start',{**PARAMS,'context':{'order_id':7001}})
        self.docker('exec','--user','10001:10001',self.original,'php','/app/app/queue-worker.php','--once')
        self.waiting=self.rpc(url,'flow.get',PARAMS)
        assert self.waiting['steps']['approve']['metadata']['deferred'],'source approval did not defer'
        self.callback={**PARAMS,'step_id':'approve','attempt':self.waiting['steps']['approve']['attempts'],
            'idempotency_key':'operator-issued-before-cutover','result':{'status':'completed'}}
        self.document(self.original,'operator-baseline.json',{'params':PARAMS,'callback':self.callback,'waiting':self.waiting})
        self.stop(self.original)
        self.helper('backup')
        restored,url=self.start_legacy('backup-restored','backup-restored')
        assert self.rpc(url,'flow.get',PARAMS)==self.waiting,'restored backup differs from stopped source'
        self.stop(restored)
        self.helper('reference-key','reference')
        self.offline('copy')
        assert self.offline('copy',check=False).returncode!=0,'existing destination was overwritten'
        uid=gid='10001'
        if self.mode!='spinner':
            uid=self.docker('run','--rm','--entrypoint','id',self.host_image,'-u','www-data').stdout.strip()
            gid=self.docker('run','--rm','--entrypoint','id',self.host_image,'-g','www-data').stdout.strip()
        self.helper('verify-copy',extra=('-e','OPERATOR_RUNTIME_UID='+uid,'-e','OPERATOR_RUNTIME_GID='+gid))
        print(self.mode+': PASS stopped legacy writer, verified backup/restore, original effect-key reference and read-only copy; existing destination refused',flush=True)

    def accept(self,host,host_token,account_token,fixture,process_count,sha,manifest,report_path=None):
        def endpoint():return 'http://127.0.0.1:'+self.mapped(host,5052)+'/workflow/rpc'
        def invoke(method,params):return self.rpc(endpoint(),method,params,account_token,host_token)
        assert invoke('flow.get',PARAMS)==self.waiting,'imported public state or pinned definition changed'
        try:self.rpc(endpoint(),'flow.get',PARAMS,LEGACY_TOKEN,host_token);raise AssertionError('old credential accepted')
        except HTTPError as error:assert error.code==401
        self.docker('restart','--timeout','10',host)
        def recovered():
            try:return process_count()==2 and invoke('flow.get',PARAMS)==self.waiting
            except (HTTPError,URLError,TimeoutError,ConnectionError,AssertionError):return False
        self.wait(recovered,'copied state did not survive actual host restart')
        invoke('flow.complete',self.callback);invoke('flow.complete',self.callback)
        completed=None;until=time.monotonic()+60
        while time.monotonic()<until:
            completed=invoke('flow.get',PARAMS)
            if completed['status']=='completed':break
            time.sleep(.1)
        assert completed['status']=='completed','copied run did not complete through Gateway'
        assert completed['run']['definition']['version']==1 and 'finish-v1' in completed['steps'] and 'finish-v2' not in completed['steps'],'selected v2 replaced pinned v1'
        assert completed['steps']['deliver']['attempts']==2 and completed['steps']['deliver']['output']['http_status']==204,'provider uncertainty was not safely replayed'
        assert completed['metadata']['execution_id']==self.waiting['metadata']['execution_id'],'physical execution identity changed'
        self.document(self.original,'operator-completed.json',completed)
        self.wait(lambda:self.helper('drained',check=False).returncode==0,'claims or outbox did not drain')
        fixture('disable')
        self.wait(lambda:process_count()==0,'Weaver writer did not stop before rollback')
        self.offline('rollback')
        self.helper('verify-rollback','rollback')
        restored,url=self.start_legacy('rollback','rollback')
        assert self.rpc(url,'flow.get',PARAMS)==completed,'old endpoint lost current progress'
        self.rpc(url,'flow.complete',self.callback)
        assert self.rpc(url,'flow.get',PARAMS)==completed,'old callback replay changed rolled-back progress'
        self.stop(restored)
        self.helper('verify-replay','rollback')
        report={'schema_version':1,'status':'representative_rehearsal_passed','dataset':'disposable','host':self.mode,
            'storage':'sqlite','package':manifest['descriptor']['identity'],'archive_sha256':sha,
            'legacy_source':'00e0bbcdd61c2c6ed96af55865d0ef2e77557adb',
            'checks':['stopped_writers','backup_restore','source_unchanged','destination_refusal','queue_claim_outbox_capacity_preserved',
                'runtime_acl','pinned_definition','new_credentials','host_restart','old_callback_deduplication','original_provider_effect_key',
                'one_provider_effect_two_requests','current_progress_rollback','legacy_endpoint_replay'],
            'production_migrated':False,'published':False}
        report['evidence']=json.loads(self.helper('report').stdout)
        assert report['evidence']['source_sha256']==report['evidence']['backup_sha256']==report['evidence']['restored_backup_sha256_before_probe'],'backup evidence changed'
        if report_path:
            fd=os.open(report_path,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
            with os.fdopen(fd,'w') as output:json.dump(report,output,indent=2);output.write('\n')
        print(self.mode+': PASS operator acceptance: original callback, pinned v1/current v2, real Gateway HTTP effect once after lost response, stopped-writer current-progress rollback and legacy replay',flush=True)
        return report
