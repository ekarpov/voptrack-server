#!/usr/bin/env python3
"""Compact only a completed, retrieved immutable CI workspace owned by this runner."""
import json
from pathlib import Path
import re
import shutil
import sys

NAME=re.compile(r'[0-9a-f]{40}-[0-9a-f]{12}')
OWNER='voptrack-immutable-ci-v1'

def compact(job, base):
    job=Path(job)
    if job.is_symlink() or job.parent != base or not NAME.fullmatch(job.name) or job.resolve()!=job:
        raise ValueError('unsafe workspace')
    marker=job/'owner.json'
    if marker.is_symlink() or json.loads(marker.read_text()) != {'owner':OWNER,'sha':job.name[:40]}:
        raise ValueError('workspace ownership mismatch')
    finished=job/'completed'
    if finished.is_symlink() or not re.fullmatch(r'[0-9]{1,3}\n',finished.read_text()):
        raise ValueError('workspace is not completed')
    # All result bytes have been retrieved before the caller invokes this mode.
    # Preserve remote logs/receipts too, and never follow a workspace symlink.
    for name in ('tree','modules','source.bundle'):
        target=job/name
        if target.is_symlink(): raise ValueError('unsafe workspace child')
    for name in ('tree','modules','source.bundle'):
        target=job/name
        if target.is_dir(): shutil.rmtree(target)
        elif target.exists(): target.unlink()
    (job/'retrieved').write_text('yes\n')

if __name__=='__main__':
    compact(sys.argv[1], Path(sys.argv[2]))
