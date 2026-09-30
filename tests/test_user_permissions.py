import os
import pathlib
import subprocess
import sys
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'backend'))
from user_permissions import default_umask


class UserPermissionsTest(unittest.TestCase):
    def test_login_defs_policy(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = pathlib.Path(tmp) / 'login.defs'
            self.assertEqual(default_umask(path), 0o022)
            for text, expected in [('# UMASK 077\n', 0o022), ('UMASK 002\n', 0o002), ('UMASK 027 # group\n', 0o027), ('UMASK 077\n', 0o077)]:
                path.write_text(text)
                self.assertEqual(default_umask(path), expected)
            for text in ['UMASK', 'UMASK +22', 'UMASK 089', 'UMASK 1777']:
                path.write_text(text)
                with self.assertRaises(ValueError):
                    default_umask(path)

    @unittest.skipIf(os.getuid() == 0, 'Run unprivileged; root acceptance runs separately on NAS')
    def test_upload_and_folder_escape_service_mask_without_changing_owner(self):
        with tempfile.TemporaryDirectory() as tmp:
            code = '''
import os,pwd,io,sys
from pathlib import Path
from unittest.mock import patch
from test_transfer import transfer, common, control
common.os = os
sys.modules["common"] = common
sys.modules["job_control"] = control
import files
root=Path(sys.argv[1])
os.umask(0o077)
with patch.object(files,'default_umask',return_value=0o027):
    files.drop(pwd.getpwuid(os.getuid()).pw_name)
(root/'folder').mkdir()
transfer.upload(root/'upload',io.BytesIO(b'test'),4)
assert (root/'folder').stat().st_mode & 0o777 == 0o750
assert (root/'upload').stat().st_mode & 0o777 == 0o640
assert (root/'upload').stat().st_uid == os.getuid()
'''
            subprocess.run([sys.executable, '-c', code, tmp], check=True,
                           env={**os.environ, 'PYTHONPATH': str(ROOT / 'backend') + ':' + str(ROOT / 'tests')})


if __name__ == '__main__':
    unittest.main()
