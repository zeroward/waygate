#!/usr/bin/env python3
"""Build only a synthetic context; check all saved context/builder/runtime layers.
Requires Docker, network for public build dependencies, and no production mounts.
Leaves uniquely named test images and temporary evidence paths for inspection.
"""
import io
import pathlib
import shutil
import subprocess
import tarfile
import tempfile
import uuid
root = pathlib.Path(__file__).resolve().parents[2]
work = pathlib.Path(tempfile.mkdtemp(prefix='waygate-build-check-'))
for name in ('cmd', 'internal', 'content'):
    # Never copy symlinks or any runtime-named files from the actual workspace.
    for src in (root/name).rglob('*'):
        if not src.is_file() or src.is_symlink():
            continue
        if any(p in ('data','.git') for p in src.relative_to(root).parts) or src.name.startswith('.env') or src.suffix in ('.key','.pem','.db','.p12','.pfx') or '.sqlite' in src.name:
            continue
        dst = work / src.relative_to(root)
        dst.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(src, dst)
for name in ('go.mod','go.sum','Dockerfile','.dockerignore'):
    shutil.copyfile(root/name, work/name)
marker = ('SYNTHETIC-EXCLUDED-' + uuid.uuid4().hex).encode()
for name in ('.env','.env.local','data/site.sqlite','data/site.sqlite-wal','data/site.sqlite-shm','data/wg/server.key','downloads/test.zip','.git/config','.agents/secret','internal/.env','internal/test.key','internal/downloads/test.zip','internal/data/secret','content/.env','content/test.pem','content/data/nested.sqlite'):
    p=work/name
    p.parent.mkdir(parents=True,exist_ok=True)
    p.write_bytes(marker)
# Proves the exact Docker ignore rules filter the context, independent of COPY paths.
with (work/'Dockerfile').open('a') as out:
    out.write('\nFROM scratch AS context-audit\nCOPY . /context\n')
run_id=uuid.uuid4().hex[:12]
for target in ('context-audit','build','waygate'):
    tag='waygate-synthetic-'+run_id+':'+target
    subprocess.run(['docker','build','--target',target,'-t',tag,str(work)],check=True)
    archive=work/(target+'.tar')
    subprocess.run(['docker','save','-o',str(archive),tag],check=True)
    files=0
    with tarfile.open(archive) as image:
        for member in image:
            if not member.isfile(): continue
            stream=image.extractfile(member)
            raw=stream.read()
            if marker in raw: raise SystemExit('Excluded fixture found in image blob: '+target)
            # Docker save may emit compressed OCI layers. Inspect recursively too.
            try:
                with tarfile.open(fileobj=io.BytesIO(raw),mode='r:*') as layer:
                    for entry in layer:
                        if entry.isfile():
                            files+=1
                            if marker in layer.extractfile(entry).read():
                                raise SystemExit('Excluded fixture found in layer: '+target)
            except tarfile.ReadError:
                pass
    if not files: raise SystemExit('No image layer files inspected for '+target)
    print('PASS:',target,'inspected',files,'layer files; excluded fixtures absent',flush=True)
print('Synthetic context and image archives:',work,flush=True)
