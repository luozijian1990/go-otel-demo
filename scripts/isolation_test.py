import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

class InitTest(unittest.TestCase):
    def test_empty_then_repeat_preserves_configuration(self):
        source=Path(__file__).resolve().parents[1]/'scripts/init-demo-env.sh'
        with tempfile.TemporaryDirectory() as d:
            root=Path(d);(root/'scripts').mkdir();target=root/'scripts/init-demo-env.sh';target.write_bytes(source.read_bytes())
            subprocess.run(['sh',str(target)],check=True,capture_output=True)
            before=(root/'.env').read_bytes()
            self.assertRegex(before.decode(),r'^DEMO_FAULT_SECRET=[0-9a-f]{64}\n$')
            self.assertEqual((root/'.env').stat().st_mode&0o777,0o600)
            subprocess.run(['sh',str(target)],check=True,capture_output=True)
            self.assertEqual((root/'.env').read_bytes(),before)
