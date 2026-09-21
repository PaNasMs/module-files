import ctypes
import errno
import json
import os
from pathlib import Path
import shutil
import stat
import tempfile

from common import require
import job_control


def publish(source, destination):
    libc = ctypes.CDLL(None, use_errno=True)
    if libc.renameat2(-100, os.fsencode(source), -100, os.fsencode(destination), 1):
        code = ctypes.get_errno()
        raise OSError(code, os.strerror(code), str(destination))


def revision(path):
    value = path.stat()
    return value.st_dev, value.st_ino, value.st_size, value.st_mtime_ns, value.st_ctime_ns


def copy_file(source, destination):
    job_control.checkpoint()
    require(stat.S_ISREG(Path(source).stat().st_mode), 'Only regular files can be copied')
    before = revision(Path(source))
    with open(source, 'rb') as src, open(destination, 'xb') as dst:
        require(stat.S_ISREG(os.fstat(src.fileno()).st_mode), 'Only regular files can be copied')
        while True:
            job_control.checkpoint()
            block = src.read(1024 * 1024)
            if not block: break
            dst.write(block)
        dst.flush()
        os.fsync(dst.fileno())
    require(revision(Path(source)) == before, 'Source changed during copying; original data was preserved')
    shutil.copystat(source, destination)
    return str(destination)


def transfer(source, destination, move=False):
    source, destination = Path(source), Path(destination)
    if move:
        try:
            publish(source, destination)
            return
        except OSError as error:
            if error.errno != errno.EXDEV: raise
    holder = Path(tempfile.mkdtemp(prefix='.panasms-copy-', dir=destination.parent))
    stage = holder / 'content'
    journal = holder / 'transfer.json'
    record = {'source': str(source), 'destination': str(destination), 'move': move, 'phase': 'copying'}
    def save():
        with (holder / 'journal.tmp').open('w') as f:
            json.dump(record, f)
            f.flush()
            os.fsync(f.fileno())
        (holder / 'journal.tmp').replace(journal)
        fd = os.open(holder, os.O_RDONLY | os.O_DIRECTORY)
        try: os.fsync(fd)
        finally: os.close(fd)
    published = False
    try:
        save()
        job_control.capability(True)
        job_control.checkpoint()
        before = revision(source)
        if source.is_dir():
            shutil.copytree(source, stage, symlinks=True, copy_function=copy_file)
        else:
            copy_file(source, stage)
        require(revision(source) == before, 'Source changed during copying; original data was preserved')
        for folder, _, _ in os.walk(stage) if stage.is_dir() else []:
            fd = os.open(folder, os.O_RDONLY | os.O_DIRECTORY)
            try: os.fsync(fd)
            finally: os.close(fd)
        job_control.capability(False)
        job_control.checkpoint()
        record['phase'] = 'publishing'
        save()
        publish(stage, destination)
        published = True
        fd = os.open(destination.parent, os.O_RDONLY | os.O_DIRECTORY)
        try: os.fsync(fd)
        finally: os.close(fd)
        record['phase'] = 'published'
        save()
        if move:
            if source.is_dir(): shutil.rmtree(source)
            else: source.unlink()
        shutil.rmtree(holder)
    except Exception:
        if not published:
            shutil.rmtree(holder)
        raise
    finally:
        job_control.capability(False)
