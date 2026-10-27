#!/usr/bin/env python3
"""Exercise real setup/reuse/rollback against a disposable Docker project."""
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import socket
import subprocess
import sys
import tempfile
import urllib.request

repo = Path(__file__).resolve().parent.parent
image = sys.argv[1] if len(sys.argv) > 1 else 'whatsapp-mcp:ci'
if not re.fullmatch(r'[a-zA-Z0-9_./:@-]+', image):
    raise SystemExit('Invalid test image reference')
project = 'wm-setup-smoke-' + str(os.getpid()) + '-' + secrets.token_hex(3)
unhealthy = project + '-unhealthy'

def run(args, **kwargs):
    return subprocess.run(args, capture_output=True, text=True, **kwargs)

def values(path):
    return dict(x.split('=', 1) for x in path.read_text().splitlines() if x and not x.startswith('#'))

with tempfile.TemporaryDirectory(prefix='wm setup smoke ') as directory:
    root = Path(directory)
    shutil.copy(repo / 'compose.yaml', root / 'compose.yaml')
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    setup = ['sh', str(repo / 'scripts/whatsapp-mcp'), 'setup', '--project', project,
             '--no-pull', '--yes']
    compose = ['docker', 'compose', '-p', project, '-f', str(root / 'compose.yaml')]
    try:
        result = run(setup + ['--image', image, '--dir', directory, '--port', str(port), '--mode', 'local'], timeout=180, cwd='/')
        assert result.returncode == 0, 'Fresh setup failed: ' + result.stderr[-1500:]
        assert 'Ready. Admin:' in result.stdout and 'Sign-in code:' in result.stdout
        original = values(root / '.env')
        assert (root / '.env').stat().st_mode & 0o777 == 0o600
        assert original['WHATSAPP_MCP_TOKEN'] not in result.stdout + result.stderr
        container = subprocess.check_output(compose + ['ps', '-q', 'whatsapp-mcp'], text=True).strip()
        previous_image = json.loads(subprocess.check_output(['docker', 'inspect', container]))[0]['Image']
        subprocess.run(['docker', 'exec', container, 'sh', '-c', 'printf setup-smoke > /data/setup-smoke-marker'], check=True)
        logo = urllib.request.urlopen('http://127.0.0.1:' + str(port) + '/admin/brand/whatsapp-mcp-logo-transparent.png').read()
        assert logo == (repo / 'assets/brand/whatsapp-mcp-logo-transparent.png').read_bytes()
        print('PASS fresh setup from an unrelated directory, healthy image, private credentials and logo')

        result = run(setup, timeout=180, cwd='/')
        assert result.returncode == 0, 'Existing deployment adoption failed: ' + result.stderr[-1500:]
        assert values(root / '.env') == original
        assert list((root / 'backups').glob('setup-*/.env'))
        print('PASS repeat setup adopts the existing directory and keeps credentials/settings')

        build = run(['docker', 'build', '-t', unhealthy, '-'], input='FROM ' + image + '\nHEALTHCHECK --interval=1s --timeout=1s --start-period=0s --retries=1 CMD false\n', timeout=120)
        assert build.returncode == 0, 'Could not build disposable failing-health fixture'
        result = run(setup + ['--image', unhealthy], timeout=300, cwd='/')
        assert result.returncode != 0 and 'previous image were restored' in result.stderr, 'Rollback did not complete: ' + result.stderr[-1500:]
        assert values(root / '.env') == original
        container = subprocess.check_output(compose + ['ps', '-q', 'whatsapp-mcp'], text=True).strip()
        obj = json.loads(subprocess.check_output(['docker', 'inspect', container]))[0]
        assert obj['Image'] == previous_image and obj['State']['Health']['Status'] == 'healthy'
        assert subprocess.check_output(['docker', 'exec', container, 'cat', '/data/setup-smoke-marker'], text=True) == 'setup-smoke'
        print('PASS unhealthy replacement rolls back to the previous healthy image and preserves volume data')
    finally:
        # Only this test's unique project, new empty-account volume, and fixture image.
        subprocess.run(compose + ['down', '--volumes'], capture_output=True, check=True)
        subprocess.run(['docker', 'image', 'rm', unhealthy], capture_output=True)
