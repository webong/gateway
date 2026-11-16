"""Verify a local release freeze against a manifest digest obtained from a trusted source."""
import argparse
import hashlib
import json
import pathlib
import tarfile


def digest(path):
    value = hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            value.update(block)
    return value.hexdigest()


def verify(root, expected):
    root = root.resolve()
    manifest_path = root / 'manifest.json'
    if manifest_path.is_symlink() or digest(manifest_path) != expected:
        raise ValueError('Manifest differs from the trusted digest')
    manifest = json.loads(manifest_path.read_text())
    if manifest['schema'] != 'gateway.release-freeze/v1' or manifest['published']:
        raise ValueError('Not an unpublished Gateway freeze')
    if {(row['runtime'], row['architecture']) for row in manifest['images']} != {(runtime, arch) for runtime in ['roadrunner', 'frankenphp'] for arch in ['amd64', 'arm64']} or len(manifest['images']) != 4:
        raise ValueError('Incomplete Router platform inventory')
    if {(row['id'], row['architecture'], row['version']) for row in manifest['extensions']} != {('webong/web', arch, manifest['version']) for arch in ['amd64', 'arm64']} or len(manifest['extensions']) != 2:
        raise ValueError('Incomplete supported extension inventory')
    declared = set()
    for item in manifest['files']:
        relative = pathlib.PurePosixPath(item['path'])
        if relative.is_absolute() or '..' in relative.parts or not relative.parts:
            raise ValueError('Unsafe freeze path')
        path = root.joinpath(*relative.parts)
        if path.is_symlink() or root not in path.resolve().parents or not path.is_file():
            raise ValueError('Missing or unsafe freeze file: ' + str(relative))
        if path.stat().st_size != item['size'] or digest(path) != item['sha256']:
            raise ValueError('Frozen file changed: ' + str(relative))
        if str(relative) in declared:
            raise ValueError('Duplicate freeze file')
        declared.add(str(relative))
    actual_assets = {p.relative_to(root).as_posix() for p in (root / 'assets').rglob('*') if p.is_file() or p.is_symlink()}
    if actual_assets != {name for name in declared if name.startswith('assets/')}:
        raise ValueError('Asset inventory differs from freeze')
    if manifest['source_archive'] not in declared:
        raise ValueError('Source archive is not covered by manifest')
    for image in manifest['images']:
        if image['archive'] not in declared:
            raise ValueError('Image archive is not covered by manifest')
        with tarfile.open(root / image['archive'], 'r:gz') as archive:
            rows = json.load(archive.extractfile('manifest.json'))
            if len(rows) != 1:
                raise ValueError('Image archive must contain exactly one image')
            config_bytes = archive.extractfile(rows[0]['Config']).read()
            if 'sha256:' + hashlib.sha256(config_bytes).hexdigest() != image['image_id']:
                raise ValueError('Image config identity changed')
            config = json.loads(config_bytes)
            labels = config['config']['Labels']
            if config['architecture'] != image['architecture'] or config['os'] != 'linux':
                raise ValueError('Image platform changed')
            if labels.get('com.webong.gateway.router-runtime') != image['runtime']:
                raise ValueError('Image runtime changed')
            if labels['org.opencontainers.image.revision'] != manifest['source_revision'] or labels['org.opencontainers.image.version'] != manifest['version']:
                raise ValueError('Image source/version changed')
    for extension in manifest['extensions']:
        if extension['archive'] not in declared:
            raise ValueError('Extension archive is not covered by manifest')
        with tarfile.open(root / extension['archive'], 'r:gz') as archive:
            names = {entry.name for entry in archive.getmembers()}
            package = extension['package_root']
            if any(entry.issym() or entry.islnk() or entry.isdev() or entry.name.startswith('/') or '..' in pathlib.PurePosixPath(entry.name).parts for entry in archive.getmembers()):
                raise ValueError('Unsafe extension archive')
            descriptor = json.load(archive.extractfile(package + '/plugin.json'))
            identity = descriptor['descriptor']['identity']
            if identity != {'id': extension['id'], 'version': extension['version'], 'revision': manifest['source_revision']}:
                raise ValueError('Extension identity changed')
            if descriptor['metadata']['gateway.spinner/v1']['platform'] != {'os': 'linux', 'arch': extension['architecture']}:
                raise ValueError('Extension descriptor platform changed')
            expected_names = {package, package + '/plugin.json'}
            for artifact in descriptor['artifacts']:
                name = package + '/' + artifact['path']
                expected_names.add(name)
                expected_names.update(parent.as_posix() for parent in pathlib.PurePosixPath(name).parents if parent.as_posix() != '.')
                payload = archive.extractfile(name).read()
                if hashlib.sha256(payload).hexdigest() != artifact['sha256']:
                    raise ValueError('Extension artifact changed')
                if artifact['path'] == 'bin/spinner':
                    machine = int.from_bytes(payload[18:20], 'little')
                    if payload[:6] != b'\x7fELF\x02\x01' or machine != {'amd64': 62, 'arm64': 183}[extension['architecture']]:
                        raise ValueError('Extension executable platform changed')
            if names != expected_names:
                raise ValueError('Extension archive inventory differs from descriptor')
    return manifest


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('directory', type=pathlib.Path)
    parser.add_argument('--expected-manifest-sha256', required=True)
    args = parser.parse_args()
    result = verify(args.directory, args.expected_manifest_sha256)
    print('PASS frozen Gateway ' + result['version'] + ': ' + str(len(result['images'])) + ' images, ' + str(len(result['extensions'])) + ' extension archives; no publication performed')
