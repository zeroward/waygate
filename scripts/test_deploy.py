import importlib.util
import json
import os
from pathlib import Path
import stat
import subprocess
import tarfile
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('deploy', Path(__file__).with_name('deploy.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class DeploymentTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        (self.root/'data').mkdir()
        (self.root/'data/kb.sqlite').write_bytes(b'synthetic database snapshot')
        (self.root/'.env').write_text('SYNTHETIC_VALUE=fixture\n')
        (self.root/'docker-compose.yml').write_text('services: {}\n')
        self.config = {'services': {'waygate': {
            'user': f'{os.getuid()}:{os.getgid()}',
            'environment': {'KB_PATH': 'data/kb.sqlite'},
            'volumes': [{'type':'bind','source':str(self.root/'data'),'target':'/app/data'}],
            'depends_on': {'clamav': {'condition':'service_started'}},
        },'clamav':{}}}
        self.calls = []
        self.stopped = False
        self.fail = None
        self.d = module.Deployment(self.root, 'docker-compose.yml', self.run_compose)

    def run_compose(self, command, **kwargs):
        args = command[len(self.d.base):]
        self.calls.append(args)
        rc = 0
        output = ''
        if args[0] == 'config': output = json.dumps(self.config)
        if args[0] == 'ps': output = 'clamav\n' if self.stopped else 'waygate\nclamav\n'
        if args[0] == 'stop': self.stopped = True
        if args[0] == self.fail or (self.fail == 'health' and '--wait' in args): rc = 1
        if args[0] == 'run':
            self.assertTrue(self.stopped)
            self.assertTrue((self.d.backup/'data.tar.gz').exists())
            self.assertEqual(stat.S_IMODE((self.root/'data').stat().st_mode),0o700)
        return subprocess.CompletedProcess(command,rc,output,'synthetic failure' if rc else '')

    def test_backup_precedes_migration_and_health_precedes_success(self):
        self.d.deploy()
        actions = [c[0] for c in self.calls]
        self.assertLess(actions.index('build'),actions.index('stop'))
        self.assertLess(actions.index('stop'),actions.index('run'))
        self.assertLess(actions.index('run'),actions.index('up'))
        self.assertIn('--wait',self.calls[-1])
        with tarfile.open(self.d.backup/'data.tar.gz') as archive:
            self.assertEqual(archive.extractfile('data/kb.sqlite').read(),b'synthetic database snapshot')
        for path in self.d.backup.iterdir():
            self.assertEqual(stat.S_IMODE(path.stat().st_mode),0o600)
        self.assertTrue((self.d.backup/'deployed').exists())

    def test_build_failure_keeps_existing_service_running(self):
        self.fail='build'
        with self.assertRaises(module.DeployError): self.d.deploy()
        self.assertFalse(self.stopped)
        self.assertIsNone(self.d.backup)

    def test_migration_failure_keeps_backup_and_does_not_start(self):
        self.fail='run'
        with self.assertRaises(module.DeployError): self.d.deploy()
        self.assertTrue(self.stopped)
        self.assertFalse(any(c[0]=='up' for c in self.calls))
        self.assertTrue((self.d.backup/'manifest.json').exists())
        self.assertFalse((self.d.backup/'deployed').exists())

    def test_health_failure_stops_new_service(self):
        self.fail='health'
        with self.assertRaises(module.DeployError): self.d.deploy()
        self.assertEqual(self.calls[-1][0],'stop')
        self.assertFalse((self.d.backup/'deployed').exists())

    def test_only_website_is_managed(self):
        self.d.deploy()
        self.assertIn(['build', 'waygate'], self.calls)
        self.assertIn(['stop', '--timeout', '30', 'waygate'], self.calls)
        self.assertFalse(any('wireguard' in c or '--remove-orphans' in c for c in self.calls))

    def test_external_database_rejected_before_stopping(self):
        self.config['services']['waygate']['environment']['KB_PATH']='/unbacked/site.sqlite'
        with self.assertRaises(module.DeployError): self.d.deploy()
        self.assertFalse(self.stopped)
        self.assertFalse(any(c[0]=='build' for c in self.calls))

    def test_symlink_rejected_before_backup(self):
        (self.root/'data/link').symlink_to(self.root/'.env')
        with self.assertRaises(module.DeployError): self.d.deploy()
        self.assertFalse(self.stopped)


if __name__=='__main__': unittest.main()
