#!/usr/bin/env python3
"""Check or repair dedicated Gatehouse secret storage while both services are stopped."""
import argparse
import os
from pathlib import Path
import stat
p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--root',type=Path,required=True,help='deployment directory containing .env')
p.add_argument('--data',type=Path,required=True,help='dedicated application data directory')
p.add_argument('--uid',type=int,required=True)
p.add_argument('--gid',type=int,required=True)
p.add_argument('--apply',action='store_true',help='repair permissions and ownership (requires owner or root)')
a=p.parse_args()
if a.uid<0 or a.gid<0: p.error('uid and gid must be nonnegative')
root=Path(os.path.abspath(a.root));data=Path(os.path.abspath(a.data))
if data in (Path('/'),Path('/tmp'),Path('/var'),root): p.error('--data must be a dedicated data directory')
paths=[]
if (root/'.env').exists() or (root/'.env').is_symlink(): paths.append(root/'.env')
if not data.exists():
    if a.apply: data.mkdir(parents=True,mode=0o700)
    else: raise SystemExit('Data directory does not exist: '+str(data))
paths.append(data)
for directory,dirs,files in os.walk(data,followlinks=False):
    paths.extend(Path(directory)/name for name in dirs+files)
# Validate everything before making changes, including non-regular objects.
for path in paths:
    st=path.lstat()
    if not (stat.S_ISDIR(st.st_mode) or stat.S_ISREG(st.st_mode)):
        raise SystemExit('Refusing non-regular private storage: '+str(path))
errors=0
for path in paths:
    st=path.lstat();mode=0o700 if stat.S_ISDIR(st.st_mode) else 0o600
    if a.apply:
        os.chmod(path,mode)
        if (st.st_uid,st.st_gid)!=(a.uid,a.gid): os.chown(path,a.uid,a.gid)
        st=path.lstat()
    if stat.S_IMODE(st.st_mode)!=mode or (st.st_uid,st.st_gid)!=(a.uid,a.gid):
        print('FAIL:',path,'expected mode',oct(mode),'owner',str(a.uid)+':'+str(a.gid));errors+=1
if errors: raise SystemExit(1)
print('PASS:',len(paths),'private paths; files 0600, directories 0700; owner',str(a.uid)+':'+str(a.gid))
