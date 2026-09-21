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
