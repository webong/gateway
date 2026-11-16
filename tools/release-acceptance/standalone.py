"""Accept an already-built standalone Router image without publishing or production data."""
import argparse
import hashlib
import json
import pathlib
import subprocess
import tempfile
import time
import uuid
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('image')
    parser.add_argument('--report', type=pathlib.Path)
    parser.add_argument('--web-package', type=pathlib.Path, help='Locally built webong%%2fweb package directory')
    args = parser.parse_args()
    root = pathlib.Path(__file__).resolve().parent
    prefix = 'gateway-release-' + uuid.uuid4().hex[:10]
    router, receiver = prefix + '-router', prefix + '-receiver'
    network, control = prefix + '-fixture', prefix + '-control'
    volumes = [prefix + '-data', prefix + '-backup', prefix + '-restored']
    token = ''

    def docker(*arguments, check=True):
        result = subprocess.run(['docker', *map(str, arguments)], capture_output=True, text=True)
        if check and result.returncode:
            detail = (result.stdout + result.stderr).replace(token, '<redacted>') if token else result.stdout + result.stderr
            raise RuntimeError('Docker ' + str(arguments[0]) + ' failed: ' + detail)
        return result

    def wait(check, message, seconds=90):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            if check():
                return
            time.sleep(.25)
        raise RuntimeError(message)

    def ledger():
        result = docker('exec', receiver, 'cat', '/tmp/received.jsonl', check=False)
        return [json.loads(line) for line in result.stdout.splitlines() if line.strip()]

    def http(path, payload=None, authenticated=False):
        headers = {'Content-Type': 'application/json'}
        if authenticated:
            headers['Authorization'] = 'Bearer ' + token
        request = Request(base + path, data=None if payload is None else json.dumps(payload).encode(), headers=headers)
        try:
            with urlopen(request, timeout=3) as response:
                return response.status, response.read()
        except HTTPError as error:
            return error.code, error.read()
        except (URLError, TimeoutError, ConnectionError):
            return 0, b''

    def deferred():
        result = docker('exec', router, 'sh', '-c',
                        '. /data/gateway.env; curl -fsS -H "Authorization: Bearer $GATEWAY_INTERNAL_TOKEN" "http://127.0.0.1:5052/deliveries?state=deferred&limit=50"', check=False)
        if result.returncode:
            return False
        return bool(json.loads(result.stdout).get('data'))

    def artisan(*arguments):
        return docker('exec', router, '/gateway/scripts/gateway-router', 'artisan', *arguments).stdout.strip()

    def stream():
        request = Request(base + '/hooks/updates', headers={'Accept': 'text/event-stream', 'Last-Event-ID': 'before-reconnect'})
        try:
            with urlopen(request, timeout=5) as response:
                body = response.read()
                return response.status == 200 and 'text/event-stream' in response.headers.get('Content-Type', '') and b'resumed=before-reconnect' in body and b'data: live-update' in body
        except (HTTPError, URLError, TimeoutError, ConnectionError):
            return False

    def start(volume):
        nonlocal base
        mounts = []
        if args.web_package:
            package_root = str(args.web_package.resolve().parent)
            mounts = ['--mount', 'type=bind,source=' + package_root + ',target=/acceptance-extensions,readonly',
                      '-e', 'GATEWAY_PLUGIN_DIR=/acceptance-extensions']
        docker('run', '-d', '--name', router, '--network', control, '--read-only', '--tmpfs', '/tmp',
               '-p', '127.0.0.1::8080', '--mount', 'type=volume,source=' + volume + ',target=/data',
               '--mount', 'type=bind,source=' + str(fixture) + ',target=/fixture,readonly',
               '-e', 'GATEWAY_ROUTER_LISTEN=:8080', '-e', 'GATEWAY_ROUTER_TLS_LISTEN=',
               '-e', 'SSL_CERT_FILE=/fixture/ca.pem', '-e', 'PHP_INI_SCAN_DIR=:/fixture', *mounts, args.image)
        docker('network', 'connect', network, router)
        port = docker('port', router, '8080/tcp').stdout.strip().split(':')[-1]
        base = 'http://127.0.0.1:' + port
        wait(lambda: http('/health')[0] == 204, 'Router did not become healthy')

    def stop():
        docker('stop', '--time', '15', router)
        assert docker('inspect', '--format', '{{.State.Running}}', router).stdout.strip() == 'false'
        docker('rm', router)

    def copy_volume(source, destination):
        # Both writers are stopped. Copy the complete data directory, including keys and spool.
        docker('run', '--rm', '--user', '0', '--entrypoint', 'sh',
               '--mount', 'type=volume,source=' + source + ',target=/source,readonly',
               '--mount', 'type=volume,source=' + destination + ',target=/destination', args.image,
               '-c', 'cp -a /source/. /destination/')
        return fingerprint(source), fingerprint(destination)

    def fingerprint(volume):
        code = '''$files=[];$it=new RecursiveIteratorIterator(new RecursiveDirectoryIterator('/snapshot',FilesystemIterator::SKIP_DOTS));foreach($it as $file){if($file->isFile()){$files[substr($file->getPathname(),10)]=hash_file('sha256',$file->getPathname());}}ksort($files);echo json_encode($files,JSON_THROW_ON_ERROR);'''
        output = docker('run', '--rm', '--user', '0', '--entrypoint', 'php', '--mount',
                        'type=volume,source=' + volume + ',target=/snapshot,readonly', args.image, '-r', code).stdout
        return hashlib.sha256(output.encode()).hexdigest()

    def event(name):
        status, response = http('/hooks/orders', {'event': 'order.created', 'acceptance': name})
        assert 200 <= status < 300, 'Webhook ingress rejected: ' + str(status) + ' ' + response.decode(errors='replace')[:500]
        wait(lambda: sum(row['payload'].get('acceptance') == name for row in ledger()) == 1,
             'Receiver did not acknowledge ' + name)

    base = ''
    try:
        with tempfile.TemporaryDirectory(prefix=prefix) as directory:
            fixture = pathlib.Path(directory)
            # Mount contains disposable server keys only. No production credentials enter the test.
            fixture.chmod(0o755)
            subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
                            '-keyout', str(fixture / 'key.pem'), '-out', str(fixture / 'ca.pem'),
                            '-subj', '/CN=release-receiver', '-addext', 'subjectAltName=DNS:release-receiver'],
                           check=True, capture_output=True)
            (fixture / 'trust.ini').write_text('openssl.cafile=/fixture/ca.pem\ncurl.cainfo=/fixture/ca.pem\n')
            docker('network', 'create', control)
            subnet = uuid.uuid4().bytes
            # Public-class addresses exercise normal SSRF checks; --internal denies external routes.
            docker('network', 'create', '--internal', '--subnet', f'11.{subnet[0]}.{subnet[1]}.0/24', network)
            for volume in volumes:
                docker('volume', 'create', volume)
            docker('run', '-d', '--name', receiver, '--network', network, '--network-alias', 'release-receiver',
                   '--read-only', '--tmpfs', '/tmp', '--mount', 'type=bind,source=' + str(fixture) + ',target=/fixture,readonly',
                   '--mount', 'type=bind,source=' + str(root) + ',target=/acceptance,readonly',
                   '--entrypoint', 'php', args.image, '/acceptance/receiver.php')
            wait(lambda: docker('exec', receiver, 'php', '-d', 'openssl.cafile=/fixture/ca.pem', '-r',
                                "exit(@file_get_contents('https://release-receiver:8443/health')==='OK'?0:1);", check=False).returncode == 0,
                 'HTTPS receiver did not start')
            start(volumes[0])
            token = artisan('gateway:token')
            assert len(token) >= 32, 'Management credential missing'
            assert http('/registry/endpoints/orders')[0] == 401, 'Unauthenticated management access accepted'
            assert http('/_internal/gateway/plan', {})[0] == 404, 'Private planning route exposed'
            artisan('gateway:endpoint', 'orders')
            artisan('gateway:subscribe', 'orders', 'acceptance-service', 'https://release-receiver:8443/events')
            assert http('/registry/endpoints/orders', authenticated=True)[0] == 200, 'Endpoint missing'
            event('fresh')
            print('PASS fresh startup, management boundaries and HTTPS webhook', flush=True)
            if args.web_package:
                manifest = json.loads((args.web_package / 'plugin.json').read_text())
                version = manifest['descriptor']['identity']['version']
                status, body = http('/extensions', {'extension': 'webong/web', 'enabled': True, 'mode': 'dynamic', 'version': version}, authenticated=True)
                assert status in (200, 201), 'Web activation rejected: ' + body.decode()[:500]
                artisan('gateway:endpoint', 'updates')
                status, body = http('/registry/endpoints/updates/subscriptions', {
                    'subscriber_id': 'stream-source', 'subscription_id': 'stream-source', 'type': 'reply',
                    'group': 'default', 'routing_scope': 'path', 'routing_key': '/hooks/updates',
                    'url': 'https://release-receiver:8443/stream'}, authenticated=True)
                assert status in (200, 201), 'Stream source rejected: ' + body.decode()[:500]
                wait(stream, 'Installed Web extension did not relay SSE with Last-Event-ID')
            if args.web_package:
                print('PASS installed Web extension and SSE', flush=True)
            stop()
            before, copied = copy_volume(volumes[0], volumes[1])
            assert before == copied, 'Stopped-volume backup differs'
            start(volumes[0])
            assert artisan('gateway:token') == token, 'Container replacement changed credentials'
            docker('exec', receiver, 'touch', '/tmp/unavailable')
            status, response = http('/hooks/orders', {'event': 'order.created', 'acceptance': 'pending'})
            assert 200 <= status < 300, 'Durable enqueue rejected: ' + str(status)
            wait(deferred, 'Receiver failure was not durably deferred')
            assert not any(row['payload'].get('acceptance') == 'pending' for row in ledger()), 'Unavailable receiver accepted effect'
            stop()
            start(volumes[0])
            assert artisan('gateway:token') == token, 'Pending recovery changed credentials'
            docker('exec', receiver, 'rm', '/tmp/unavailable')
            wait(lambda: sum(row['payload'].get('acceptance') == 'pending' for row in ledger()) == 1,
                 'Pending delivery did not recover after container replacement')
            print('PASS pending delivery recovered after replacement', flush=True)
            event('replacement')
            stop()
            backup, restored = copy_volume(volumes[1], volumes[2])
            assert backup == restored == before, 'Restore differs from immutable backup'
            start(volumes[2])
            assert artisan('gateway:token') == token, 'Restore changed credentials'
            assert http('/registry/endpoints/orders', authenticated=True)[0] == 200, 'Restore lost registry'
            event('restored')
            if args.web_package:
                wait(stream, 'Web activation or SSE source did not survive restore')
            rows = ledger()
            assert sorted(row['payload']['acceptance'] for row in rows) == ['fresh', 'pending', 'replacement', 'restored'], 'Unexpected replay'
            assert all(row['event_id'] for row in rows), 'Stable HTTP delivery identity missing'
            identity = json.loads(docker('image', 'inspect', args.image).stdout)[0]
            report = {'status': 'passed', 'image_id': identity['Id'], 'architecture': identity['Architecture'],
                      'backup_sha256': before, 'delivered_events': len(rows),
                      'checks': ['fresh_start', 'private_routes', 'management_auth', 'operator_cli', 'https_webhook_delivery',
                                 'container_replacement', 'pending_delivery_recovered', 'stopped_volume_backup_restore', 'credentials_and_registry_retained', 'no_completed_replay'],
                      'published': False, 'production_data': False,
                      'limits': ['same-version replacement and restore; not a cross-version schema rollback',
                                 'SQLite and local spool; shared PostgreSQL is a separate gate']}
            if args.web_package:
                report['checks'].extend(['installed_web_extension', 'https_sse_stream', 'sse_last_event_id', 'extension_activation_restored'])
            if args.report:
                with args.report.open('x') as output:
                    json.dump(report, output, indent=2)
                    output.write('\n')
            print(json.dumps(report, indent=2), flush=True)
    except Exception:
        result = docker('logs', '--tail', '25', router, check=False)
        print((result.stdout + result.stderr).replace(token, '<redacted>') if token else 'Router acceptance failed', flush=True)
        raise
    finally:
        for container in (router, receiver):
            docker('rm', '-f', container, check=False)
        for volume in volumes:
            docker('volume', 'rm', volume, check=False)
        for item in (network, control):
            docker('network', 'rm', item, check=False)


if __name__ == '__main__':
    main()
