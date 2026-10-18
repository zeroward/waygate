#!/usr/bin/env python3
"""Deploy local Compose images with an offline, backed-up SQLite migration."""
import argparse
import fcntl
import json
import os
from pathlib import Path, PurePosixPath
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile


class DeployError(Exception):
    pass


def private_paths(data):
    if not data.exists():
        if data.is_symlink():
            raise DeployError('Data directory is a symlink')
        return []
    paths = [data]
    for directory, dirs, files in os.walk(data, followlinks=False):
        paths.extend(Path(directory) / name for name in dirs + files)
    for path in paths:
        mode = path.lstat().st_mode
        if not (stat.S_ISREG(mode) or stat.S_ISDIR(mode)):
            raise DeployError('Private storage contains a non-regular path: ' + str(path))
    return paths


class Deployment:
    def __init__(self, root, compose_file, runner=subprocess.run):
        self.root = root.resolve()
        self.compose_file = compose_file
        self.runner = runner
        self.base = ([shutil.which('docker-compose')] if shutil.which('docker-compose') else ['docker', 'compose']) + ['--project-directory', str(self.root), '-f', str(self.root / compose_file)]
        self.backup = None

    def compose(self, *args, capture=False):
        # Resolved Compose configuration includes secrets. Capture it; never print it.
        result = self.runner(self.base + list(args), cwd=self.root, check=False,
                             capture_output=True, text=True)
        if result.returncode:
            if self.backup:
                error_log = self.backup / 'last-error.log'
                error_log.write_text(result.stdout + result.stderr)
                error_log.chmod(0o600)
            # Compose errors can contain expanded environment values. Keep them out
            # of deploy output; the operator can inspect the named stage separately.
            raise DeployError('Compose stage failed: ' + args[0])
        if capture:
            return result.stdout
        return ''

    def inspect(self):
        config = json.loads(self.compose('config', '--format', 'json', capture=True))
        self.services = config['services']
        web = self.services['waygate']
        try:
            self.uid, self.gid = map(int, web['user'].split(':'))
        except (KeyError, ValueError):
            raise DeployError('waygate.user must be an explicit numeric UID:GID')
        if self.uid < 0 or self.gid < 0:
            raise DeployError('Invalid data owner')
        mounts = {v['target']: v for v in web.get('volumes', [])}
        mount = mounts.get('/app/data', {})
        if mount.get('type') != 'bind':
            raise DeployError('Deployment requires a dedicated bind mount at /app/data')
        self.data = Path(mount['source'])
        if not self.data.is_absolute():
            self.data = self.root / self.data
        # Refuse shared roots and symlinked ancestors before touching permissions.
        if self.data in (self.root, Path('/'), Path('/tmp'), Path('/var')) or self.data != self.data.resolve():
            raise DeployError('Data must be a dedicated directory without symlinked ancestors')
        env = web.get('environment', {})
        db = PurePosixPath(env.get('KB_PATH') or 'data/kb.sqlite')
        if not db.is_absolute():
            db = PurePosixPath('/app') / db
        if '..' in db.parts or not db.is_relative_to('/app/data') or db == PurePosixPath('/app/data'):
            raise DeployError('KB_PATH must be a file inside /app/data for this deployment command')
        self.db = str(db)
        for path in private_paths(self.data):
            st = path.stat()
            if os.geteuid() != 0 and (st.st_uid != self.uid or st.st_gid != self.gid or self.uid != os.geteuid()):
                raise DeployError('Existing data ownership requires root; run this command with sudo')
            if not os.access(path, os.R_OK | (os.X_OK if path.is_dir() else 0)):
                raise DeployError('Cannot back up private data; run this command with sudo')
        envfile = self.root / '.env'
        if envfile.is_symlink():
            raise DeployError('.env must not be a symlink')
        if envfile.exists() and not os.access(envfile, os.R_OK | os.W_OK):
            raise DeployError('Cannot protect .env; run this command as its owner or root')
        return {'database': self.db, 'data': str(self.data), 'owner': f'{self.uid}:{self.gid}'}

    def save_backup(self):
        parent = self.root / '.deployment-backups'
        if parent.is_symlink():
            raise DeployError('Backup directory must not be a symlink')
        parent.mkdir(mode=0o700, exist_ok=True)
        parent.chmod(0o700)
        if parent.resolve().is_relative_to(self.data):
            raise DeployError('Backup directory must be outside application data')
        self.backup = Path(tempfile.mkdtemp(prefix='deploy-', dir=parent))
        private_paths(self.data)  # Revalidate after stopping all writers.
        with tarfile.open(self.backup / 'data.tar.gz', 'w:gz', dereference=False) as archive:
            if self.data.exists():
                archive.add(self.data, arcname='data')
        (self.backup / 'data.tar.gz').chmod(0o600)
        if (self.root / '.env').exists():
            shutil.copyfile(self.root / '.env', self.backup / '.env')
            (self.backup / '.env').chmod(0o600)
        shutil.copyfile(self.root / self.compose_file, self.backup / 'compose.yml')
        (self.backup / 'compose.yml').chmod(0o600)
        manifest = {'database': self.db, 'data': str(self.data), 'uid': self.uid, 'gid': self.gid,
                    'previously_running': sorted(self.running), 'previous_images': self.previous_images, 'status': 'backup-complete'}
        (self.backup / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
        (self.backup / 'manifest.json').chmod(0o600)
        # Flush completed backup files before allowing database mutation.
        for path in self.backup.iterdir():
            with path.open('rb') as file:
                os.fsync(file.fileno())
        fd = os.open(self.backup, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)
        print('Private backup:', self.backup, flush=True)

    def repair(self):
        self.data.mkdir(mode=0o700, parents=True, exist_ok=True)
        for path in private_paths(self.data):
            path.chmod(0o700 if path.is_dir() else 0o600)
            st = path.stat()
            if (st.st_uid, st.st_gid) != (self.uid, self.gid):
                os.chown(path, self.uid, self.gid)
        if (self.root / '.env').exists():
            (self.root / '.env').chmod(0o600)

    def deploy(self):
        summary = self.inspect()
        print('Deployment:', json.dumps(summary), flush=True)
        self.running = set(self.compose('ps', '--status', 'running', '--services', capture=True).split())
        managed = ['waygate']
        self.previous_images = {name: self.compose('images', '-q', name, capture=True).split() for name in managed}
        print('Building images before downtime…', flush=True)
        self.compose('build', *managed)
        print('Stopping website database writers…', flush=True)
        self.compose('stop', '--timeout', '30', *managed)
        remaining = set(self.compose('ps', '--status', 'running', '--services', capture=True).split())
        if remaining.intersection(managed):
            raise DeployError('A managed service is still running; migration was not started')
        try:
            self.save_backup()
            self.repair()
            print('Running offline migration…', flush=True)
            self.compose('run', '--rm', '--no-deps', '-T', 'waygate', 'migrate', '--db', self.db)
            # Start only the app's declared dependencies; do not restart the realm.
            dependencies = list(self.services['waygate'].get('depends_on', {}))
            if dependencies:
                self.compose('up', '-d', '--no-build', *dependencies)
            print('Starting website and waiting for health…', flush=True)
            self.compose('up', '-d', '--no-deps', '--no-build', '--wait', '--wait-timeout', '120', 'waygate')
        except Exception:
            # A failed health check may leave a restarted container running.
            self.compose('stop', '--timeout', '30', *managed)
            raise
        (self.backup / 'deployed').write_text('Migration and website health check succeeded.\n')
        (self.backup / 'deployed').chmod(0o600)
        print('Deployment complete; website healthy. Backup:', self.backup, flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--compose-file', default='docker-compose.yml')
    parser.add_argument('--check', action='store_true', help='validate resolved paths/ownership without deploying')
    args = parser.parse_args()
    os.umask(0o077)
    root = Path(__file__).resolve().parents[1]
    deploy = Deployment(root, args.compose_file)
    try:
        # Do not follow a deployment lock planted as a symlink.
        fd = os.open(root / '.deploy.lock', os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'w') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            if args.check:
                print(json.dumps(deploy.inspect(), indent=2))
            else:
                deploy.deploy()
    except (DeployError, OSError, ValueError, KeyError) as err:
        print('Deployment stopped:', err, file=sys.stderr)
        if deploy.backup:
            print('Backup:', deploy.backup, file=sys.stderr)
            print('Services remain stopped. Fix the cause and rerun; no automatic database rollback.', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
