#!/usr/bin/env python3
"""Copy source to a fresh temporary directory and run synthetic assessment tests."""
import argparse
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
mode = parser.add_mutually_exclusive_group()
mode.add_argument('--race', action='store_true')
mode.add_argument('--fuzz', action='store_true')
args = parser.parse_args()
root = Path(__file__).resolve().parents[2]
go = shutil.which('go')
if go is None:
    raise SystemExit('Go must be available on PATH; race mode also requires a C compiler.')
dest = Path(tempfile.mkdtemp(prefix='waygate-security-repro-'))
files = subprocess.check_output(['git', 'ls-files', '-z'], cwd=root).decode().split('\0')
# Include new hardening source/tests before a commit as well.
files += [str(p.relative_to(root)) for base in ('internal', 'cmd') for p in (root / base).rglob('*') if p.is_file()]
for name in files:
    if not name:
        continue
    # Explicitly exclude runtime material even if accidentally tracked later.
    path = Path(name)
    if path.parts[0] in ('.git', 'data', 'downloads', 'bin') or path.name == '.env' or path.suffix in ('.key', '.pub'):
        continue
    source = root / path
    if source.is_file() and not source.is_symlink():
        target = dest / path
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, target)
for source in (Path(__file__).parent / 'repro').glob('*_test.go.txt'):
    name = source.name.removesuffix('_test.go.txt')
    package = 'web' if name == 'passkey' else name
    shutil.copyfile(source, dest / 'internal' / package / ('assessment_' + name + '_test.go'))
print('Synthetic test directory:', dest, flush=True)
cmd = [go, 'test']
if args.fuzz:
    cmd += ['./internal/downloads', '-run', '^$', '-fuzz', 'FuzzAssessmentSafeRelPath', '-fuzztime=10s', '-parallel=2']
else:
    if args.race:
        cmd.append('-race')
    cmd += ['./internal/...', '-run', 'TestAssessment', '-count=1', '-v']
raise SystemExit(subprocess.call(cmd, cwd=dest, env=os.environ.copy()))
