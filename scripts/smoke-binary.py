#!/usr/bin/env python3
"""Exercise a packaged binary using temporary credentials and data only."""
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import re
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

binary = str(Path(sys.argv[1]).resolve())
runner = sys.argv[2:]
with tempfile.TemporaryDirectory(prefix='yunx-smoke-') as tmp:
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    env = dict(os.environ, YUNX_ADDR=f'127.0.0.1:{port}', YUNX_DATA_DIR=tmp+'/data', YUNX_DOWNLOAD_DIR=tmp+'/downloads', YUNX_ADMIN_PASSWORD='binary-test-password')
    proc = subprocess.Popen(runner+[binary], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
    base = f'http://127.0.0.1:{port}'
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    try:
        deadline = time.monotonic()+30
        while True:
            try:
                with opener.open(base+'/health', timeout=1) as response:
                    assert json.load(response)['status']=='ok'
                break
            except (OSError, urllib.error.URLError):
                if time.monotonic()>deadline or proc.poll() is not None:
                    raise RuntimeError('Binary did not become healthy')
                time.sleep(.1)
        try:
            opener.open(base+'/api/state')
            raise AssertionError('Unauthenticated state allowed')
        except urllib.error.HTTPError as error:
            assert error.code==401
        request=urllib.request.Request(base+'/api/auth/login',data=json.dumps({'username':'admin','password':'binary-test-password'}).encode(),headers={'Content-Type':'application/json','X-YunX-Request':'1'})
        with opener.open(request) as response:
            assert json.load(response)['ok']
        with opener.open(base+'/api/state') as response:
            state=json.load(response)
            assert state['tasks']==[] and len(state['platforms'])==(5 if env.get('YUNX_PROVIDER_JAR')=='disabled' else 18)
        with opener.open(base+'/') as response:
            assert response.headers.get_content_type()=='text/html'
            html=response.read().decode()
        expected=Path(__file__).resolve().parents[1]/'internal/webassets/dist'
        if expected.exists():
            assert html==(expected/'index.html').read_text(), 'Embedded HTML differs from frozen frontend'
        assets=re.findall(r'(?:src|href)="(/assets/[^"\s]+)"',html)
        assert len(assets)>=2
        for asset in assets:
            with opener.open(base+asset) as response:
                content=response.read()
                assert response.status==200 and len(content)>100
                if expected.exists():
                    assert hashlib.sha256(content).digest()==hashlib.sha256((expected/asset.lstrip('/')).read_bytes()).digest(), 'Embedded asset hash differs'
        icons=sorted(p.stem for p in (expected/'icons').glob('*.png'))
        assert len(icons)==19
        for icon in icons:
            asset=f'/icons/{icon}.png'
            with opener.open(base+asset) as response:
                assert response.headers.get_content_type()=='image/png'
                content=response.read()
                assert content.startswith(b'\x89PNG\r\n\x1a\n')
                assert content==(expected/asset.lstrip('/')).read_bytes(), 'Embedded icon differs from original'
        assert 'href="/icons/yunx.png"' in html
        print(json.dumps({'binary':Path(binary).name,'runner':runner,'health':True,'auth':True,'embedded_assets':len(assets),'embedded_icons':len(icons),'platforms':len(state['platforms']),'favicon_mime':True,'embedded_hash_match':expected.exists()}))
    finally:
        proc.send_signal(signal.SIGTERM)
        try:
            _,err=proc.communicate(timeout=20)
        except subprocess.TimeoutExpired:
            proc.kill();proc.communicate();raise AssertionError('Graceful shutdown timed out')
        assert proc.returncode==0,err.decode(errors='replace')
