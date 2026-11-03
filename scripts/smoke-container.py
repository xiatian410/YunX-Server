#!/usr/bin/env python3
"""Exercise a release image through the shipped Compose configuration on its native runner.

All directories, credentials, networks and the HTTP source are disposable test fixtures.
Waits have deadlines; this script does not change product polling or retry behavior.
"""
import hashlib
import http.cookiejar
import http.server
import json
import os
from pathlib import Path
import re
import secrets
import socket
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
IMAGE, VERSION = sys.argv[1:3]
PAYLOAD = b'YunX native container download fixture\n' * 8192

class Source(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_HEAD(self):
        self.respond(False)

    def do_GET(self):
        self.respond(True)

    def respond(self, body):
        if self.path != '/fixture.bin':
            self.send_error(404)
            return
        match = re.fullmatch(r'bytes=(\d+)-(\d*)', self.headers.get('Range', ''))
        start = int(match[1]) if match else 0
        end = min(int(match[2]) if match and match[2] else len(PAYLOAD)-1, len(PAYLOAD)-1)
        if start > end:
            self.send_response(416)
            self.send_header('Content-Range', f'bytes */{len(PAYLOAD)}')
            self.end_headers()
            return
        self.send_response(206 if match else 200)
        self.send_header('Content-Type', 'application/octet-stream')
        self.send_header('ETag', '"release-fixture-v1"')
        self.send_header('Accept-Ranges', 'bytes')
        self.send_header('Content-Length', str(end-start+1))
        if match:
            self.send_header('Content-Range', f'bytes {start}-{end}/{len(PAYLOAD)}')
        self.end_headers()
        if body:
            self.wfile.write(PAYLOAD[start:end+1])

def wait_for(check, seconds):
    deadline = time.monotonic() + seconds
    while True:
        result = check()
        if result:
            return result
        if time.monotonic() >= deadline:
            raise TimeoutError(f'Fixture condition exceeded {seconds}s')
        time.sleep(.5)

source = http.server.ThreadingHTTPServer(('0.0.0.0', 0), Source)
threading.Thread(target=source.serve_forever, daemon=True).start()
try:
    with tempfile.TemporaryDirectory(prefix='yunx-image-') as tmp:
        path = Path(tmp)
        for directory in ('data', 'downloads'):
            (path / directory).mkdir()
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        password = secrets.token_urlsafe(32)
        envfile = path / 'fixture.env'
        envfile.write_text(f'YUNX_IMAGE={IMAGE}\nPUID={os.getuid()}\nPGID={os.getgid()}\nDATA_DIR={path}/data\nDOWNLOAD_DIR={path}/downloads\nBIND_IP=127.0.0.1\nPORT={port}\nALLOW_PRIVATE_DOWNLOADS=true\nPUBLIC_URL=\nFIXTURE_ADMIN_PASSWORD={password}\n')
        envfile.chmod(0o600)
        override = path / 'fixture.yaml'
        override.write_text('services:\n  yunx:\n    environment:\n      YUNX_ADMIN_PASSWORD: "${FIXTURE_ADMIN_PASSWORD}"\n    extra_hosts:\n      - "host.docker.internal:host-gateway"\n')
        command = ['docker', 'compose', '--env-file', str(envfile), '-p', 'yunx-smoke-'+secrets.token_hex(5), '-f', str(ROOT/'compose.yaml'), '-f', str(override)]
        def compose(*args, timeout=60):
            return subprocess.check_output(command + list(args), text=True, stderr=subprocess.STDOUT, timeout=timeout)
        opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        base = f'http://127.0.0.1:{port}'
        def request(route, data=None):
            req = urllib.request.Request(base+route, data=None if data is None else json.dumps(data).encode(), headers={'Content-Type':'application/json','X-YunX-Request':'1'})
            return opener.open(req, timeout=5)
        def api(route, data=None):
            with request(route, data) as response:
                return json.load(response)
        def healthy():
            try:
                return api('/health')['status'] == 'ok'
            except (OSError, urllib.error.URLError):
                return False
        def login():
            assert api('/api/auth/login', {'username':'admin','password':password})['ok']
        try:
            config = json.loads(compose('config', '--format', 'json'))['services']['yunx']
            assert 'build' not in config and config['read_only']
            assert config['user'] == f'{os.getuid()}:{os.getgid()}'
            assert config['cap_drop'] == ['ALL']
            compose('up', '-d', '--no-build', '--pull', 'never')
            wait_for(healthy, 60)
            try:
                request('/api/state')
                raise AssertionError('Unauthenticated state allowed')
            except urllib.error.HTTPError as error:
                assert error.code == 401
            login()
            state = api('/api/state')
            assert len(state['platforms']) == 18 and state['version'] == VERSION
            with request('/') as response:
                html = response.read().decode()
            frozen = json.loads((ROOT/'docs/design/freeze.json').read_text())['files']
            assets = re.findall(r'(?:src|href)="(/assets/[^"\s]+)"', html)
            assert len(assets) == 2
            for asset in ['/', *assets]:
                with request(asset) as response:
                    content = response.read()
                filename = 'index.html' if asset == '/' else asset.lstrip('/')
                assert hashlib.sha256(content).hexdigest() == frozen['internal/webassets/dist/'+filename]
            icons = list((ROOT/'web/public/icons').glob('*.png'))
            assert len(icons) == 19
            for icon in icons:
                with request('/icons/'+icon.name) as response:
                    assert response.headers.get_content_type() == 'image/png'
                    assert response.read() == icon.read_bytes()
            versions = {}
            for name, args in {'java':['java','-version'], 'node':['node','--version'], 'ffmpeg':['ffmpeg','-version'], 'ffprobe':['ffprobe','-version']}.items():
                versions[name] = compose('exec','-T','yunx',*args).splitlines()[0]
            browser_code = '''import {chromium} from '/opt/yunx/node_modules/playwright-core/index.mjs';
import {mkdtempSync,rmSync} from 'node:fs';
const dir=mkdtempSync('/tmp/yunx-release-browser-');let b;
try{b=await chromium.launch({executablePath:process.env.YUNX_CHROMIUM,headless:true,env:{...process.env,XDG_CONFIG_HOME:dir,XDG_CACHE_HOME:dir},args:['--no-sandbox','--disable-dev-shm-usage']});const p=await b.newPage();await p.setContent('<p id="fixture">YunX</p>');if(await p.locator('#fixture').textContent()!=='YunX')throw Error('Browser fixture failed');console.log(b.version())}finally{if(b)await b.close();rmSync(dir,{recursive:true,force:true})}'''
            versions['chromium'] = compose('exec','-T','yunx','node','--input-type=module','-e',browser_code, timeout=35).strip()
            parsed = api('/api/resolve', {'text':f'http://host.docker.internal:{source.server_port}/fixture.bin'})
            tasks = api('/api/tasks', {'session_id':parsed['session_id'],'item_ids':[parsed['items'][0]['id']],'directory':'release-test'})
            task_id = tasks['tasks'][0]['id']
            def downloaded():
                task = next(t for t in api('/api/state')['tasks'] if t['id'] == task_id)
                assert task['status'] not in ('failed','waiting_account','cancelled'), task.get('error', task['status'])
                return task if task['status'] == 'completed' else False
            task = wait_for(downloaded, 45)
            assert (path/'downloads/release-test'/task['name']).read_bytes() == PAYLOAD
            with request('/api/tasks/'+task_id+'/file') as response:
                assert response.read() == PAYLOAD
            compose('restart', 'yunx')
            wait_for(healthy, 60)
            login()
            restored = next(t for t in api('/api/state')['tasks'] if t['id'] == task_id)
            assert restored['status'] == 'completed' and restored['has_file']
            container = compose('ps','-q','yunx').strip()
            def docker_healthy():
                status = subprocess.check_output(['docker','inspect','--format','{{.State.Health.Status}}',container],text=True).strip()
                assert status != 'unhealthy'
                return status == 'healthy'
            wait_for(docker_healthy, 45)
            summary = {'image':IMAGE,'version':VERSION,'architecture':compose('exec','-T','yunx','uname','-m').strip(),'compose':True,'non_root':os.getuid()!=0,'read_only':True,'healthcheck':True,'platforms':18,'icons':19,'frozen_web_assets':True,'http_download_sha256':hashlib.sha256(PAYLOAD).hexdigest(),'restart_persistence':True,'runtime':versions}
            print(json.dumps(summary, ensure_ascii=False))
        except Exception:
            print(compose('logs','--no-color','--tail','80'), file=sys.stderr)
            raise
        finally:
            compose('down','--volumes','--remove-orphans', timeout=60)
finally:
    source.shutdown()
    source.server_close()
