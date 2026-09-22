import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

from test_transfer import transfer


@unittest.skipUnless(os.geteuid() == 0, 'Requires root to exercise distinct Linux identities')
class IdentityTest(unittest.TestCase):
    def as_user(self, uid, gid, groups, action):
        read, write = os.pipe()
        child = os.fork()
        if child == 0:
            os.close(read)
            try:
                os.setgroups(groups); os.setgid(gid); os.setuid(uid)
                action(); result = 'ok'
            except Exception as error:
                result = type(error).__name__
            os.write(write, json.dumps(result).encode()); os.close(write); os._exit(0)
        os.close(write)
        with os.fdopen(read) as stream: result = json.load(stream)
        os.waitpid(child, 0)
        return result

    def test_other_identity_cannot_read_or_upload_into_private_folder(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); root.chmod(0o711)
            home = root / 'private'; home.mkdir(mode=0o700); os.chown(home, 65534, 65534)
            self.assertEqual(self.as_user(65534, 65534, [], lambda: transfer.upload(home / 'file', io.BytesIO(b'private'), 7)), 'ok')
            self.assertEqual(self.as_user(65533, 65533, [], lambda: transfer.download(home / 'file', io.BytesIO())), 'PermissionError')
            self.assertEqual(self.as_user(65533, 65533, [], lambda: transfer.upload(home / 'other', io.BytesIO(b'x'), 1)), 'PermissionError')

    @unittest.skipUnless(shutil.which('setfacl'), 'Requires Linux ACL utilities')
    def test_upload_inherits_shared_group_and_default_acl(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); root.chmod(0o711)
            shared = root / 'shared'; shared.mkdir(); os.chown(shared, 0, 65532); shared.chmod(0o2770)
            subprocess.run(['setfacl', '-m', 'd:u::rwx,d:g::rwx,d:o::---', str(shared)], check=True)
            self.assertEqual(self.as_user(65534, 65534, [65532], lambda: transfer.upload(shared / 'file', io.BytesIO(b'shared'), 6)), 'ok')
            self.assertEqual((shared / 'file').stat().st_gid, 65532)
            self.assertEqual((shared / 'file').stat().st_mode & 0o777, 0o660)
            self.assertEqual(self.as_user(65533, 65533, [65532], lambda: (shared / 'file').write_bytes(b'edited')), 'ok')
            self.assertEqual(self.as_user(65533, 65533, [], lambda: transfer.download(shared / 'file', io.BytesIO())), 'PermissionError')
