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


if __name__ == '__main__':
    unittest.main()
