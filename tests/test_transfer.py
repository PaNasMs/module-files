import errno
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

import types

control = types.ModuleType('job_control')
control.Cancelled = type('Cancelled', (Exception,), {})
control.capability = lambda allowed: None
control.checkpoint = lambda: None
common = types.ModuleType('common')
def require(value, message):
    if not value: raise ValueError(message)
common.require = require
sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'backend'))
with patch.dict(sys.modules, {'job_control': control, 'common': common}):
    import transfer
job_control = control


class TransferTest(unittest.TestCase):
    def test_complete_tree_published_with_links_and_metadata(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); source = root / 'source'; source.mkdir()
            (source / 'file').write_bytes(b'content' * 10000)
            (source / 'file').chmod(0o640)
            (source / 'link').symlink_to('file')
            destination = root / 'destination'
            transfer.transfer(source, destination)
            self.assertEqual((destination / 'file').read_bytes(), (source / 'file').read_bytes())
            self.assertTrue((destination / 'link').is_symlink())
            self.assertEqual((destination / 'file').stat().st_mode & 0o777, 0o640)
            self.assertEqual(list(root.glob('.panasms-copy-*')), [])

    def test_cancel_removes_staging_and_keeps_source(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); source = root / 'source'; source.write_bytes(b'x' * 3000000)
            destination = root / 'destination'
            with patch.object(job_control, 'checkpoint', side_effect=[None, None, None, job_control.Cancelled()]):
                with self.assertRaises(job_control.Cancelled): transfer.transfer(source, destination)
            self.assertEqual(source.stat().st_size, 3000000)
            self.assertFalse(destination.exists())
            self.assertEqual(list(root.glob('.panasms-copy-*')), [])

    def test_existing_destination_is_never_overwritten(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); source = root / 'source'; source.write_text('original')
            destination = root / 'destination'; destination.write_text('other')
            for move in (False, True):
                with self.assertRaises(FileExistsError): transfer.transfer(source, destination, move=move)
                self.assertEqual(destination.read_text(), 'other')
                self.assertEqual(source.read_text(), 'original')
            self.assertEqual(list(root.glob('.panasms-copy-*')), [])

    def test_cross_device_move_keeps_published_copy_on_cleanup_failure(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); source = root / 'source'; source.write_text('original')
            destination = root / 'destination'
            publish = transfer.publish
            def cross_device(src, dst):
                if src == source: raise OSError(errno.EXDEV, 'Cross-device')
                return publish(src, dst)
            unlink = Path.unlink
            def fail_source(path, *args, **kwargs):
                if path == source: raise PermissionError('Source became read-only')
                return unlink(path, *args, **kwargs)
            with patch.object(transfer, 'publish', side_effect=cross_device), patch.object(Path, 'unlink', fail_source):
                with self.assertRaises(PermissionError): transfer.transfer(source, destination, move=True)
            self.assertEqual(source.read_text(), 'original')
            self.assertEqual(destination.read_text(), 'original')
            self.assertEqual(len(list(root.glob('.panasms-copy-*/transfer.json'))), 1)

class TransferIntegrityTest(unittest.TestCase):
    def test_nested_change_after_copy_prevents_publication(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); source = root / 'source'; source.mkdir()
            nested = source / 'nested'; nested.mkdir()
            original = nested / 'file'; original.write_text('before')
            copytree = transfer.shutil.copytree
            def changed(*args, **kwargs):
                result = copytree(*args, **kwargs)
                original.write_text('after!')
                return result
            with patch.object(transfer.shutil, 'copytree', side_effect=changed):
                with self.assertRaisesRegex(ValueError, 'Source changed'):
                    transfer.transfer(source, root / 'destination')
            self.assertFalse((root / 'destination').exists())
            self.assertEqual(original.read_text(), 'after!')

    def test_cross_device_cleanup_rechecks_source(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); source = root / 'source'; source.write_text('before')
            destination = root / 'destination'; publish = transfer.publish
            def changed(src, dst):
                if src == source: raise OSError(errno.EXDEV, 'Cross-device')
                publish(src, dst)
                source.write_text('after!')
            with patch.object(transfer, 'publish', side_effect=changed):
                with self.assertRaisesRegex(ValueError, 'both copies'):
                    transfer.transfer(source, destination, move=True)
            self.assertEqual(source.read_text(), 'after!')
            self.assertEqual(destination.read_text(), 'before')
            self.assertEqual(len(list(root.glob('.panasms-copy-*/transfer.json'))), 1)

    def test_special_files_and_symlink_swaps_do_not_block(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); source = root / 'source'; os.mkfifo(source)
            with self.assertRaisesRegex(ValueError, 'regular'):
                transfer.copy_file(source, root / 'destination')
            source.unlink(); source.symlink_to(root / 'private')
            (root / 'private').write_text('secret')
            with self.assertRaises(OSError): transfer.copy_file(source, root / 'other')

    def test_upload_respects_umask_and_never_exposes_partial_content(self):
        import io
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); destination = root / 'file'
            class Stream(io.BytesIO):
                def read(self, size=-1):
                    self_test.assertFalse(destination.exists())
                    holders = list(root.glob('.panasms-upload-*'))
                    self_test.assertEqual(len(holders), 1)
                    self_test.assertEqual(holders[0].stat().st_mode & 0o777, 0o700)
                    return super().read(size)
            self_test = self; old = os.umask(0o027)
            try: transfer.upload(destination, Stream(b'content'), 7)
            finally: os.umask(old)
            self.assertEqual(destination.read_bytes(), b'content')
            self.assertEqual(destination.stat().st_mode & 0o777, 0o640)
            self.assertEqual(list(root.glob('.panasms-upload-*')), [])

    def test_short_oversized_and_disconnected_uploads_leave_no_file(self):
        import io
        with tempfile.TemporaryDirectory() as tmp:
            destination = Path(tmp) / 'file'
            for content in (b'ab', b'abcd'):
                with self.assertRaises(ValueError): transfer.upload(destination, io.BytesIO(content), 3)
                self.assertFalse(destination.exists())
            stream = unittest.mock.Mock()
            stream.read.side_effect = [b'ab', ConnectionResetError('Disconnected')]
            with self.assertRaises(ConnectionResetError): transfer.upload(destination, stream, 3)
            self.assertEqual(list(Path(tmp).iterdir()), [])

    def test_concurrent_uploads_publish_one_complete_winner(self):
        import io
        import threading
        from concurrent.futures import ThreadPoolExecutor
        with tempfile.TemporaryDirectory() as tmp:
            destination = Path(tmp) / 'file'; barrier = threading.Barrier(2)
            publish = transfer.publish
            def together(src, dst):
                barrier.wait(timeout=5)
                return publish(src, dst)
            def run(data):
                try:
                    transfer.upload(destination, io.BytesIO(data), len(data))
                    return 'ok'
                except FileExistsError: return 'conflict'
            with patch.object(transfer, 'publish', side_effect=together), ThreadPoolExecutor(2) as pool:
                results = list(pool.map(run, [b'a' * 2000000, b'b' * 2000000]))
            self.assertCountEqual(results, ['ok', 'conflict'])
            self.assertIn(destination.read_bytes(), (b'a' * 2000000, b'b' * 2000000))
            self.assertEqual(len(list(Path(tmp).iterdir())), 1)

    def test_download_rejects_same_size_modification(self):
        import io
        with tempfile.TemporaryDirectory() as tmp:
            source = Path(tmp) / 'file'; source.write_bytes(b'original')
            class Output(io.BytesIO):
                def write(self, data):
                    if not data.startswith(b'OK '): source.write_bytes(b'changed!')
                    return super().write(data)
            with self.assertRaisesRegex(ValueError, 'Source changed during download'):
                transfer.download(source, Output())

    def test_large_upload_reads_bounded_chunks(self):
        with tempfile.TemporaryDirectory() as tmp:
            destination = Path(tmp) / 'large'
            class Stream:
                remaining = 64 * 1024 * 1024 + 17
                def read(self, size):
                    if not 0 < size <= 1024 * 1024: raise AssertionError('Unbounded read')
                    count = min(size, self.remaining); self.remaining -= count
                    return b'x' * count
            transfer.upload(destination, Stream(), Stream.remaining)
            self.assertEqual(destination.stat().st_size, Stream.remaining)

    def test_no_space_during_sync_leaves_no_published_upload(self):
        import io
        with tempfile.TemporaryDirectory() as tmp:
            destination = Path(tmp) / 'file'
            with patch.object(transfer.os, 'fsync', side_effect=OSError(errno.ENOSPC, 'No space')):
                with self.assertRaises(OSError): transfer.upload(destination, io.BytesIO(b'abc'), 3)
            self.assertEqual(list(Path(tmp).iterdir()), [])

    def test_killed_upload_leaves_only_private_staging_and_allows_retry(self):
        import io
        import multiprocessing
        import signal
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); destination = root / 'file'
            context = multiprocessing.get_context('fork'); ready = context.Event()
            def killed_upload():
                class Stream:
                    def read(self, size):
                        ready.set()
                        signal.pause()
                transfer.upload(destination, Stream(), 1024)
            process = context.Process(target=killed_upload); process.start()
            try:
                self.assertTrue(ready.wait(5))
                os.kill(process.pid, signal.SIGKILL); process.join(5)
                self.assertFalse(destination.exists())
                holder, = root.glob('.panasms-upload-*')
                self.assertEqual(holder.stat().st_mode & 0o777, 0o700)
                transfer.upload(destination, io.BytesIO(b'retry'), 5)
                self.assertEqual(destination.read_bytes(), b'retry')
            finally:
                if process.is_alive(): process.kill()
                process.join()
