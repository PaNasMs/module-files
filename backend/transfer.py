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


def stat_revision(value):
    return value.st_dev, value.st_ino, value.st_size, value.st_mtime_ns, value.st_ctime_ns


def revision(path):
    return stat_revision(path.lstat())


def sync_directory(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try: os.fsync(fd)
    finally: os.close(fd)


def snapshot(path):
    entries = {}
    def visit(current):
        job_control.checkpoint()
        info = current.lstat()
        require(stat.S_ISREG(info.st_mode) or stat.S_ISDIR(info.st_mode) or stat.S_ISLNK(info.st_mode),
                'Only regular files, folders and symbolic links can be copied')
        entries[str(current.relative_to(path))] = stat_revision(info)
        if stat.S_ISDIR(info.st_mode):
            for child in current.iterdir(): visit(child)
    visit(path)
    return entries


def copy_file(source, destination):
    job_control.checkpoint()
    before = revision(Path(source))
    fd = os.open(source, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, 'rb') as src, open(destination, 'xb') as dst:
        require(stat.S_ISREG(os.fstat(src.fileno()).st_mode), 'Only regular files can be copied')
        require(stat_revision(os.fstat(fd)) == before, 'Source changed before copying')
        while True:
            job_control.checkpoint()
            block = src.read(1024 * 1024)
            if not block: break
            dst.write(block)
        dst.flush()
        os.fsync(dst.fileno())
        require(stat_revision(os.fstat(fd)) == before, 'Source changed during copying; original data was preserved')
    require(revision(Path(source)) == before, 'Source changed during copying; original data was preserved')
    shutil.copystat(source, destination)
    return str(destination)


def transfer(source, destination, move=False):
    source, destination = Path(source), Path(destination)
    if move:
        try:
            publish(source, destination)
            for parent in {source.parent, destination.parent}: sync_directory(parent)
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
        before = snapshot(source)
        if source.is_dir():
            shutil.copytree(source, stage, symlinks=True, copy_function=copy_file)
        else:
            copy_file(source, stage)
        require(snapshot(source) == before, 'Source changed during copying; original data was preserved')
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
            require(snapshot(source) == before, 'Source changed before cleanup; both copies were preserved')
            if source.is_dir(): shutil.rmtree(source)
            else: source.unlink()
            sync_directory(source.parent)
        shutil.rmtree(holder)
    except Exception:
        if not published:
            shutil.rmtree(holder)
        raise
    finally:
        job_control.capability(False)


def upload(destination, stream, expected):
    require(expected >= 0, 'Upload size is required')
    destination = Path(destination)
    holder = Path(tempfile.mkdtemp(prefix='.panasms-upload-', dir=destination.parent))
    stage = holder / 'content'
    try:
        with stage.open('xb') as output:
            remaining = expected
            while remaining:
                block = stream.read(min(1024 * 1024, remaining))
                require(bool(block), 'Incomplete transfer')
                output.write(block)
                remaining -= len(block)
            require(not stream.read(1), 'Upload exceeds declared size')
            output.flush()
            os.fsync(output.fileno())
        publish(stage, destination)
        sync_directory(destination.parent)
    finally:
        shutil.rmtree(holder)


def download(target, output):
    fd = os.open(target, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, 'rb') as source:
        info = os.fstat(fd)
        require(stat.S_ISREG(info.st_mode), 'Not a regular file')
        output.write(f'OK {info.st_size}\n'.encode())
        output.flush()
        remaining = info.st_size
        while remaining:
            block = source.read(min(1024 * 1024, remaining))
            require(bool(block), 'Source was truncated during download; retry the download')
            output.write(block)
            remaining -= len(block)
        require(stat_revision(os.fstat(fd)) == stat_revision(info), 'Source changed during download; retry the download')
