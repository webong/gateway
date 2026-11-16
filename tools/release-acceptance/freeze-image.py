"""Export a local versioned image once; never build, tag, push or upload an image."""
import argparse
import gzip
import hashlib
import json
import pathlib
import shutil
import subprocess
import tempfile


def digest(path):
    value = hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            value.update(block)
    return value.hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('directory', type=pathlib.Path)
    parser.add_argument('runtime', choices=['roadrunner', 'frankenphp'])
    parser.add_argument('architecture', choices=['amd64', 'arm64'])
    parser.add_argument('image')
    args = parser.parse_args()
    root = args.directory.resolve()
    selection = json.loads((root / 'selection.json').read_text())
    version, source = selection['version'], selection['source_revision']
    image = json.loads(subprocess.check_output(['docker', 'image', 'inspect', args.image]))[0]
    labels = image['Config']['Labels']
    if image['Architecture'] != args.architecture or image['Os'] != 'linux':
        raise ValueError('Image architecture does not match selection')
    if labels.get('org.opencontainers.image.revision') != source or labels.get('org.opencontainers.image.version') != version or labels.get('com.webong.gateway.router-runtime') != args.runtime:
        raise ValueError('Image source, version or runtime does not match selection')
    assets = root / 'assets'
    assets.mkdir(exist_ok=True)
    name = f'gateway-router-{version}-{args.runtime}-linux-{args.architecture}.docker.tar.gz'
    destination = assets / name
    if destination.exists():
        raise FileExistsError('Frozen image archive already exists')
    with tempfile.TemporaryDirectory(prefix='gateway-image-export-') as directory:
        raw = pathlib.Path(directory) / 'image.tar'
        subprocess.run(['docker', 'image', 'save', '--output', str(raw), args.image], check=True)
        with destination.open('xb') as output, raw.open('rb') as incoming:
            with gzip.GzipFile(filename='', mode='wb', fileobj=output, mtime=0, compresslevel=6) as compressed:
                shutil.copyfileobj(incoming, compressed)
    row = {'runtime': args.runtime, 'architecture': args.architecture, 'image_id': image['Id'],
           'local_reference': args.image, 'archive': 'assets/' + name, 'archive_sha256': digest(destination),
           'rootfs_diff_ids': image['RootFS']['Layers'],
           'registry_reference': 'webong/gateway:v' + version + ('-frankenphp' if args.runtime == 'frankenphp' else ''),
           'registry_digest': None, 'published': False}
    row_path = root / (args.runtime + '-' + args.architecture + '-image.json')
    with row_path.open('x') as output:
        json.dump(row, output, indent=2)
        output.write('\n')
    # Verify the exported bytes can be restored without rebuilding the image.
    subprocess.run(['docker', 'image', 'load', '--input', str(destination)], check=True, capture_output=True)
    loaded = json.loads(subprocess.check_output(['docker', 'image', 'inspect', args.image]))[0]
    if loaded['Id'] != image['Id']:
        raise ValueError('Reloaded image identity differs from export')
    print(json.dumps(row, indent=2))


if __name__ == '__main__':
    main()
