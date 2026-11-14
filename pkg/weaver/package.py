"""Verify a locally built Weaver artifact and prepare deterministic release archives.
No registry pushes, Git tags or release publication occur here.
"""
import argparse,gzip,hashlib,json,pathlib,re,subprocess,tarfile,tempfile,uuid

parser=argparse.ArgumentParser()
parser.add_argument('image');parser.add_argument('destination',type=pathlib.Path)
parser.add_argument('--asset-base',help='Reviewed HTTPS release-asset base URL; omitted for local consumer rehearsal')
parser.add_argument('--release',action='store_true',help='Require a clean source revision and release version')
args=parser.parse_args();source=pathlib.Path(__file__).resolve().parent
container='weaver-package-'+uuid.uuid4().hex[:12]
def docker(*values):return subprocess.check_output(['docker',*map(str,values)],text=True).strip()
def digest(path):return hashlib.sha256(path.read_bytes()).hexdigest()
def archive(root,target):
    with target.open('wb') as output:
        with gzip.GzipFile(filename='',mode='wb',fileobj=output,mtime=0) as zipped:
            with tarfile.open(fileobj=zipped,mode='w',format=tarfile.USTAR_FORMAT) as tar:
                for path in [root,*sorted(root.rglob('*'))]:
                    assert not path.is_symlink(),'Archive symlinks are forbidden'
                    info=tar.gettarinfo(str(path),arcname=str(path.relative_to(root.parent)))
                    info.uid=info.gid=0;info.uname=info.gname='';info.mtime=0
                    if path.is_file():
                        with path.open('rb') as data:tar.addfile(info,data)
                    else:tar.addfile(info)

with tempfile.TemporaryDirectory(prefix='weaver-package-') as temporary:
    temporary=pathlib.Path(temporary);package=temporary/'webong%2fgateway-weaver'
    try:
        docker('create','--name',container,args.image);docker('cp',container+':/package',package)
    finally:subprocess.run(['docker','rm','-f',container],capture_output=True)
    manifest=json.loads((package/'plugin.json').read_text());identity=manifest['descriptor']['identity']
    assert identity['id']=='webong/gateway-weaver'
    planner=manifest['metadata']['gateway.planner/v1'];assert planner['composer']==identity['id'] and planner['version']==identity['version']
    assert manifest['metadata']['gateway.weaver/v1']['source']=='278861082e00537fe7e759eb7c8bee3e7a3f0bf4'
    assert manifest['metadata']['gateway.weaver/v1']['surfaces']==['core']
    arch=manifest['metadata']['gateway.spinner/v1']['platform']['arch'];assert arch in ('amd64','arm64')
    if args.release:
        assert args.asset_base and args.asset_base.startswith('https://'),'Release requires a reviewed HTTPS asset base'
        assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=source,text=True).strip()==identity['revision']
        assert not subprocess.check_output(['git','status','--porcelain'],cwd=source,text=True).strip(),'Release source must be clean'
        assert manifest['metadata']['gateway.weaver/v1']['runtimeImage']=='ghcr.io/webong/workflow@sha256:dc876bfa06d295e4049a5658ef813d56a536db22c069da674633151664813d9e','Reviewed runtime digest required'
        assert re.fullmatch(r'[a-f0-9]{40}',identity['revision']),'Release requires a clean source commit'
        assert re.fullmatch(r'\d+\.\d+\.\d+(?:-rc\.\d+)?',identity['version']),'Release version required'
    expected={'plugin.json'};names={}
    for artifact in manifest['artifacts']:
        relative=artifact['path'];path=pathlib.PurePosixPath(relative)
        assert not path.is_absolute() and '..' not in path.parts and str(path)==relative
        assert relative not in expected and artifact['name'] not in names
        expected.add(relative);names[artifact['name']]=relative
        assert digest(package/relative)==artifact['sha256'],'Artifact integrity mismatch: '+relative
    actual=set()
    for path in package.rglob('*'):
        assert not path.is_symlink(),'Artifact symlink forbidden'
        if path.is_file():actual.add(path.relative_to(package).as_posix())
    assert actual==expected,'Unlisted or missing packaged files'
    assert any(entry['name']=='spinner' and names[entry['artifact']]=='bin/spinner' for entry in manifest['entrypoints'])
    args.destination.mkdir(parents=True,exist_ok=True)
    target=args.destination/('gateway-weaver-linux-'+arch+'.tar.gz')
    assert not target.exists(),'Refusing to replace an existing artifact'
    archive(package,target);repeat=temporary/'repeat.tar.gz';archive(package,repeat)
    assert digest(target)==digest(repeat),'Archive bytes are not deterministic'
    # A separate Composer source dist avoids installing bundled runtime/vendor files.
    composer_root=temporary/'gateway-weaver-composer';composer_root.mkdir()
    import shutil
    for name in ('src','schema','bin','routes','register.php','composer.json','LICENSE','README.md','UPGRADE.md','HTTP.md'):
        path=source/name
        if path.is_dir():
            for member in path.rglob('*'):
                if member.is_file():
                    counterpart=package/member.relative_to(source)
                    assert counterpart.is_file() and digest(member)==digest(counterpart),'Composer/runtime source mismatch: '+str(member.relative_to(source))
            shutil.copytree(path,composer_root/name)
        else:shutil.copy2(path,composer_root/name)
    composer_document=json.loads((composer_root/'composer.json').read_text());composer_document['version']=identity['version']
    (composer_root/'composer.json').write_text(json.dumps(composer_document,indent=2)+'\n')
    composer_target=args.destination/('gateway-weaver-composer-'+arch+'.tar.gz')
    assert not composer_target.exists();archive(composer_root,composer_target)
    base=args.asset_base.rstrip('/') if args.asset_base else 'file:///release'
    assert base.startswith('https://') or not args.asset_base,'Release asset base must use HTTPS'
    composer_document['dist']={'type':'tar','url':base+'/'+composer_target.name,'reference':identity['revision'],'shasum':hashlib.sha1(composer_target.read_bytes()).hexdigest()}
    repository_package={key:value for key,value in composer_document.items() if key not in ('repositories','config','scripts','minimum-stability','prefer-stable','require-dev','autoload-dev')}
    (args.destination/('packages-'+arch+'.json')).write_text(json.dumps({'packages':{identity['id']:{identity['version']:repository_package}}},indent=2)+'\n')
    (args.destination/('composer-weaver-'+arch+'.json')).write_text(json.dumps(composer_document,indent=2)+'\n')
    report={'identity':identity,'build_inputs':manifest['metadata']['gateway.weaver/v1'],'platform':manifest['metadata']['gateway.spinner/v1']['platform'],
            'sha256':{target.name:digest(target),composer_target.name:digest(composer_target)}}
    (args.destination/('SHA256SUMS-'+arch)).write_text(''.join(value+'  '+name+'\n' for name,value in report['sha256'].items()))
    (args.destination/('weaver-'+arch+'.json')).write_text(json.dumps(report,indent=2)+'\n')
    print(json.dumps(report,indent=2))
