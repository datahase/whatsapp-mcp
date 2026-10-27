#!/usr/bin/env python3
"""Check host CLI behavior without touching real Docker containers."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPTS = Path(__file__).resolve().parent


class HostCLI(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="whatsapp cli ")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.calls = self.root / "calls.jsonl"
        docker = self.bin / "docker"
        docker.write_text("""#!/usr/bin/env python3
import json,os,sys
args=sys.argv[1:]
with open(os.environ['CLI_TEST_CALLS'],'a') as f:f.write(json.dumps(args)+'\\n')
if args[0]=='info':sys.exit(int(os.environ.get('CLI_TEST_DOCKER_FAILURE','0')))
if args[:2]==['ps','-aq']:print(os.environ.get('CLI_TEST_IDS','abc123'),end='')
elif args[0]=='inspect' and '--format' in args:print('full123')
elif args[0]=='exec':print('synthetic sign-in code')
""")
        docker.chmod(0o755)
        self.env = dict(os.environ, PATH=str(self.bin) + os.pathsep + os.environ['PATH'], CLI_TEST_CALLS=str(self.calls))
        self.env.pop('WHATSAPP_MCP_CONTAINER', None)

    def pipe_install(self, prefix, mode='valid'):
        curl = self.bin / 'curl'
        curl.write_text("""#!/usr/bin/env python3
import json,os,pathlib,sys
args=sys.argv[1:]
with open(os.environ['CLI_TEST_CALLS'],'a') as f:f.write(json.dumps(args)+'\\n')
mode=os.environ['CLI_TEST_CURL_MODE']
if mode=='fail':sys.exit(22)
out=pathlib.Path(args[args.index('--output')+1])
out.write_text(pathlib.Path(os.environ['CLI_TEST_SOURCE']).read_text() if mode=='valid' else '<html>Not found</html>')
""")
        curl.chmod(0o755)
        env = dict(self.env, CLI_TEST_SOURCE=str(SCRIPTS / 'whatsapp-mcp'), CLI_TEST_CURL_MODE=mode, WHATSAPP_MCP_REF='test-ref')
        return subprocess.run(['sh', '-s', '--', str(prefix)], input=(SCRIPTS / 'install-cli.sh').read_text(), cwd='/', env=env, capture_output=True, text=True)

    def cli(self, *args):
        return subprocess.run(['sh', str(SCRIPTS / 'whatsapp-mcp'), *args], cwd='/', env=self.env, capture_output=True, text=True)

    def recorded(self):
        return [json.loads(x) for x in self.calls.read_text().splitlines()] if self.calls.exists() else []

    def test_login_from_unrelated_directory(self):
        result = self.cli('login')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.recorded()[-1], ['exec', 'abc123', 'whatsapp-mcp', 'login-code'])

    def test_multiple_instances_require_selection(self):
        self.env['CLI_TEST_IDS'] = 'abc123\ndef456\n'
        result = self.cli('restart')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Multiple', result.stderr)
        self.assertFalse(any(x[0] == 'restart' for x in self.recorded()))

    def test_explicit_container_and_log_arguments(self):
        result = self.cli('--container', 'selected', 'logs', '--follow')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.recorded()[-1], ['logs', '--tail', '50', '--follow', 'selected'])

    def test_missing_container(self):
        self.env['CLI_TEST_IDS'] = ''
        result = self.cli('login')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('No WhatsApp MCP container', result.stderr)

    def test_docker_unavailable(self):
        self.env['CLI_TEST_DOCKER_FAILURE'] = '1'
        result = self.cli('stop')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Cannot access Docker', result.stderr)
        self.assertEqual(self.recorded(), [['info']])

    def test_unknown_command_never_accesses_docker(self):
        self.assertNotEqual(self.cli('delete').returncode, 0)
        self.assertEqual(self.recorded(), [])

    def test_install_is_independent_of_checkout(self):
        prefix = self.root / 'installed cli'
        result = subprocess.run(['sh', str(SCRIPTS / 'install-cli.sh'), str(prefix)], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        installed = prefix / 'bin/whatsapp-mcp'
        self.assertFalse(installed.is_symlink())
        result = subprocess.run([str(installed), 'login'], cwd='/', env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('synthetic sign-in code', result.stdout)

    def test_installer_preserves_existing_executable(self):
        target = self.bin / 'whatsapp-mcp'
        target.write_text('existing binary placeholder')
        result = subprocess.run(['sh', str(SCRIPTS / 'install-cli.sh'), str(self.root)], capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(target.read_text(), 'existing binary placeholder')

    def test_piped_installer_downloads_selected_ref(self):
        prefix = self.root / 'piped installation'
        result = self.pipe_install(prefix)
        self.assertEqual(result.returncode, 0, result.stderr)
        target = prefix / 'bin/whatsapp-mcp'
        self.assertEqual(target.read_bytes(), (SCRIPTS / 'whatsapp-mcp').read_bytes())
        self.assertTrue(os.access(target, os.X_OK))
        self.assertIn('https://raw.githubusercontent.com/datahase/whatsapp-mcp/test-ref/scripts/whatsapp-mcp', self.recorded()[0])
        self.assertIn('=https', self.recorded()[0])

    def test_failed_download_preserves_existing_installation(self):
        target = self.bin / 'whatsapp-mcp'
        target.write_bytes((SCRIPTS / 'whatsapp-mcp').read_bytes())
        before = target.read_bytes()
        result = self.pipe_install(self.root, 'fail')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(target.read_bytes(), before)

    def test_invalid_download_is_not_installed(self):
        prefix = self.root / 'invalid installation'
        result = self.pipe_install(prefix, 'invalid')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Invalid CLI', result.stderr)
        self.assertFalse((prefix / 'bin/whatsapp-mcp').exists())

    def setup_tools(self):
        shim = '''#!/usr/bin/env python3
import os,sys,json,pathlib
name=pathlib.Path(sys.argv[0]).name;args=sys.argv[1:]
with open(os.environ['CLI_TEST_CALLS'],'a') as f:f.write(json.dumps({'tool':name,'args':args})+'\\n')
fail=os.environ.get('CLI_TEST_SETUP_FAIL','')
if name=='id':print('0');sys.exit(0)
if name=='curl':
 if fail=='download':sys.exit(22)
 source=os.environ['CLI_TEST_SOURCE'] if any(x.endswith('/scripts/whatsapp-mcp') for x in args) else os.environ['CLI_TEST_COMPOSE_SOURCE']
 pathlib.Path(args[args.index('--output')+1]).write_bytes(pathlib.Path(source).read_bytes());sys.exit(0)
if name=='tailscale':
 if args[:2]==['ip','-4']:
  if fail=='tailscale':sys.exit(1)
  print('100.64.0.1')
 elif args[:3]==['serve','status','--json']:print(os.environ.get('CLI_TEST_TS_CONFIG','{}'))
 elif '--bg' in args:print('Available:\\nhttps://setup-example.tailnet.ts.net/\\n')
 sys.exit(0)
if name=='docker':
 if args[:2]==['ps','-aq']:print(os.environ.get('CLI_TEST_SETUP_IDS','existing123' if os.environ.get('CLI_TEST_EXISTING_DIR') else ''),end='')
 elif args[0]=='inspect':print(os.environ.get('CLI_TEST_EXISTING_DIR',''))
 elif args[0]=='pull' and fail=='pull':sys.exit(1)
 elif args[0]=='compose':
  if 'run' in args and fail=='config':sys.exit(1)
  if 'up' in args and fail=='startup':sys.exit(1)
  if 'exec' in args:print('Sign-in code: SYNTHETIC-ONLY')
'''
        for name in ['docker', 'curl', 'tailscale', 'id']:
            tool = self.bin / name
            tool.write_text(shim)
            tool.chmod(0o755)
        self.env['CLI_TEST_COMPOSE_SOURCE'] = str(SCRIPTS.parent / 'compose.yaml')
        self.env['CLI_TEST_SOURCE'] = str(SCRIPTS / 'whatsapp-mcp')
        self.env.pop('SUDO_USER', None)
        return self.root / 'deployment'

    def setup_run(self, directory, *args):
        return self.cli('setup', '--dir', str(directory), '--yes', *args)

    def env_values(self, directory):
        return dict(line.split('=', 1) for line in (directory / '.env').read_text().splitlines() if line and not line.startswith('#'))

    def test_setup_fresh_local(self):
        directory = self.setup_tools()
        result = self.setup_run(directory, '--mode', 'local')
        self.assertEqual(result.returncode, 0, result.stderr)
        values = self.env_values(directory)
        self.assertEqual(len(values['WHATSAPP_MCP_TOKEN']), 64)
        self.assertNotEqual(values['WHATSAPP_MCP_TOKEN'], values['WHATSAPP_MCP_ADMIN_PASSWORD'])
        self.assertNotIn(values['WHATSAPP_MCP_TOKEN'], result.stdout)
        self.assertEqual((directory / '.env').stat().st_mode & 0o777, 0o600)
        self.assertEqual(values['BIND_IP'], '127.0.0.1')
        self.assertEqual(values['AI_ENABLED'], 'false')
        self.assertIn('http://127.0.0.1:8765/admin', result.stdout)
        self.assertTrue(any(x['tool']=='docker' and '--wait' in x['args'] and '--no-build' in x['args'] for x in self.recorded()))
        self.assertFalse((directory / '.setup-lock').exists())

    def test_setup_imports_advanced_env(self):
        directory = self.setup_tools()
        settings = self.root / 'advanced.env'
        settings.write_text('AI_ENABLED=true\nAI_API_KEY=synthetic-provider-key\nAI_DAILY_REQUESTS=20\n')
        result = self.setup_run(directory, '--env-file', str(settings))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.env_values(directory)['AI_API_KEY'], 'synthetic-provider-key')
        self.assertEqual(self.env_values(directory)['AI_ENABLED'], 'true')
        self.assertNotIn('synthetic-provider-key', result.stdout + result.stderr)

    def test_setup_adopts_existing_project_and_preserves_settings(self):
        directory = self.setup_tools()
        self.assertEqual(self.setup_run(directory, '--mode', 'local').returncode, 0)
        original = self.env_values(directory)
        self.env['CLI_TEST_EXISTING_DIR'] = str(directory)
        result = self.cli('setup', '--yes', '--mode', 'local')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.env_values(directory), original)
        self.assertTrue(list((directory / 'backups').glob('setup-*/.env')))

    def test_setup_rejects_other_existing_project_directory(self):
        directory = self.setup_tools()
        original = self.root / 'original'
        original.mkdir();(original / '.env').write_text('WHATSAPP_MCP_TOKEN=keep-this-original-secret\n')
        self.env['CLI_TEST_EXISTING_DIR'] = str(original)
        directory.mkdir();(directory / '.env').write_text('OTHER=true\n')
        result = self.setup_run(directory)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('different directory', result.stderr)
        self.assertEqual((original / '.env').read_text(), 'WHATSAPP_MCP_TOKEN=keep-this-original-secret\n')

    def test_setup_config_failure_restores_existing_env(self):
        directory = self.setup_tools()
        self.assertEqual(self.setup_run(directory).returncode, 0)
        before = (directory / '.env').read_bytes()
        self.calls.write_text('');self.env['CLI_TEST_SETUP_FAIL'] = 'config'
        result = self.setup_run(directory, '--image', 'example/changed:1')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((directory / '.env').read_bytes(), before)
        self.assertFalse(any(x['tool']=='docker' and 'up' in x['args'] for x in self.recorded()))

    def test_setup_rejects_occupied_tailscale_port(self):
        directory = self.setup_tools()
        self.env['CLI_TEST_TS_CONFIG'] = '{"TCP":{"443":{"HTTPS":true}}}'
        result = self.setup_run(directory, '--mode', 'funnel')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('already uses HTTPS port', result.stderr)
        self.assertFalse(any(x['tool']=='tailscale' and ('--bg' in x['args'] or x['args'][0]=='funnel') for x in self.recorded()))

    def test_setup_public_funnel_only_after_oauth_container_ready(self):
        directory = self.setup_tools()
        result = self.setup_run(directory, '--mode', 'funnel')
        self.assertEqual(result.returncode, 0, result.stderr)
        values = self.env_values(directory)
        self.assertEqual(values['WHATSAPP_MCP_OAUTH_ONLY'], 'true')
        self.assertEqual(values['WHATSAPP_MCP_PUBLIC_URL'], 'https://setup-example.tailnet.ts.net')
        events = self.recorded()
        up = next(i for i,x in enumerate(events) if x['tool']=='docker' and 'up' in x['args'])
        public = next(i for i,x in enumerate(events) if x['tool']=='tailscale' and x['args'][0]=='funnel')
        self.assertLess(up, public)
        before_up = [x for x in events[:up] if x['tool']=='tailscale' and '--bg' in x['args']]
        self.assertTrue(all(x['args'][-1].startswith('text:') for x in before_up))

    def test_setup_failed_startup_never_enables_funnel(self):
        directory = self.setup_tools();self.env['CLI_TEST_SETUP_FAIL'] = 'startup'
        result = self.setup_run(directory, '--mode', 'funnel')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any(x['tool']=='tailscale' and x['args'][0]=='funnel' for x in self.recorded()))
        self.assertTrue(any(x['tool']=='tailscale' and x['args'][-1]=='off' for x in self.recorded()))

    def test_setup_download_failure_leaves_no_credentials_or_container(self):
        directory = self.setup_tools();self.env['CLI_TEST_SETUP_FAIL'] = 'download'
        result = self.setup_run(directory)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((directory / '.env').exists())
        self.assertFalse(any(x['tool']=='docker' and 'up' in x['args'] for x in self.recorded()))

    def test_setup_custom_proxy_requires_origin(self):
        directory = self.setup_tools()
        bad = self.setup_run(directory, '--mode', 'https', '--url', 'https://example.com/mcp')
        self.assertNotEqual(bad.returncode, 0)
        good = self.setup_run(directory, '--mode', 'https', '--url', 'https://example.com')
        self.assertEqual(good.returncode, 0, good.stderr)
        self.assertEqual(self.env_values(directory)['WHATSAPP_MCP_PUBLIC_URL'], 'https://example.com')

    def test_installer_can_launch_setup_without_checkout(self):
        directory = self.setup_tools()
        result = subprocess.run(['sh', '-s', '--', str(self.root / 'installed'), '--setup', '--dir', str(directory), '--yes'], input=(SCRIPTS / 'install-cli.sh').read_text(), cwd='/', env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((directory / '.env').exists())


    def test_installer_setup_returns_to_invoking_user(self):
        directory = self.setup_tools()
        sudo = self.bin / 'sudo'
        sudo.write_text("#!/usr/bin/env python3\nimport os,json,sys\nwith open(os.environ['CLI_TEST_CALLS'],'a') as f:f.write(json.dumps({'tool':'sudo','args':sys.argv[1:]})+'\\n')\n")
        sudo.chmod(0o755)
        self.env['SUDO_USER'] = 'setup-owner'
        prefix = self.root / 'system install'
        result = subprocess.run(['sh', str(SCRIPTS / 'install-cli.sh'), str(prefix), '--setup', '--dir', str(directory), '--yes'], env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        event = next(x for x in self.recorded() if x['tool']=='sudo')
        self.assertEqual(event['args'][:3], ['-H', '-u', 'setup-owner'])
        self.assertEqual(event['args'][3:5], [str(prefix / 'bin/whatsapp-mcp'), 'setup'])


if __name__ == '__main__':
    unittest.main()
