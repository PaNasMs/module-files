import ctypes
import errno
import hashlib
import json
import os
from pathlib import Path
import shutil
import stat
import tempfile

from common import require, Rejected
import job_control


def publish(source, destination, flags=1):
    libc = ctypes.CDLL(None, use_errno=True)
    if libc.renameat2(-100, os.fsencode(source), -100, os.fsencode(destination), flags):
        code = ctypes.get_errno()
        raise OSError(code, os.strerror(code), str(destination))


class ReplacementUncertain(Rejected):
    pass


def replacement_revision(path):
    value = Path(path).lstat()
    # Renaming changes ctime; this identity must also match after atomic exchange.
    data = [value.st_dev, value.st_ino, value.st_mode, value.st_size, value.st_mtime_ns]
    return hashlib.sha256(json.dumps(data).encode()).hexdigest()


def commit_destination(stage, destination, expected=None):
    if not expected:
        publish(stage, destination)
        return
    require(not destination.is_symlink(), 'Symbolic links cannot be replaced')
    require(stage.is_dir() == destination.is_dir(), 'File and folder types do not match; rename or skip this item')
    require(replacement_revision(destination) == expected, 'Destination changed; choose what to do again')
    staged_revision = replacement_revision(stage)
    publish(stage, destination, 2)
    restored = False
    try:
        if replacement_revision(stage) == expected:
            return
        if replacement_revision(destination) == staged_revision:
            publish(stage, destination, 2)
            restored = True
    except Exception as error:
        raise ReplacementUncertain('Could not verify replacement; previous data retained at ' + str(stage)) from error
    if restored:
        require(False, 'Destination changed; choose what to do again')
    raise ReplacementUncertain('Concurrent destination change; previous data retained at ' + str(stage))


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


def transfer(source, destination, move=False, replace_revision=None):
    source, destination = Path(source), Path(destination)
    require(source != destination and source not in destination.parents and destination not in source.parents,
            'Source and destination must not contain each other')
    if destination.exists():
        require(not os.path.samefile(source, destination), 'Source and destination are the same item')
    if move and not replace_revision:
        try:
            publish(source, destination)
            for parent in {source.parent, destination.parent}: sync_directory(parent)
            return
        except OSError as error:
            if error.errno != errno.EXDEV: raise
    holder = Path(tempfile.mkdtemp(prefix='.panasms-copy-', dir=destination.parent))
    stage = holder / 'content'
    journal = holder / 'transfer.json'
    record = {'source': str(source), 'destination': str(destination), 'move': move, 'phase': 'copying', 'replace_revision': replace_revision}
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
        commit_destination(stage, destination, replace_revision)
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
    except Exception as error:
        if not published and not isinstance(error, ReplacementUncertain):
            shutil.rmtree(holder)
        raise
    finally:
        job_control.capability(False)


def upload(destination, stream, expected, replace_revision=None):
    require(expected >= 0, 'Upload size is required')
    destination = Path(destination)
    holder = Path(tempfile.mkdtemp(prefix='.panasms-upload-', dir=destination.parent))
    stage = holder / 'content'
    preserve = False
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
        commit_destination(stage, destination, replace_revision)
        sync_directory(destination.parent)
    except ReplacementUncertain:
        preserve = True
        raise
    finally:
        if not preserve: shutil.rmtree(holder)


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
