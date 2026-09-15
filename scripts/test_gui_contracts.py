import subprocess
import sys
import tempfile
from pathlib import Path
import unittest


class GUIContractRunnerTests(unittest.TestCase):
    def test_collects_unittest_classes_and_plain_test_functions(self):
        with tempfile.TemporaryDirectory() as root:
            tests = Path(root) / 'tests'
            tests.mkdir()
            (tests / 'test_fixture.py').write_text('''import unittest
import os
class Fixture(unittest.TestCase):
    def test_class(self): pass

def test_function():
    assert False, "function contract was actually executed"

def test_monkeypatch(monkeypatch):
    monkeypatch.setenv("ORKA_TEST_FIXTURE_ONLY", "set")
    assert os.getenv("ORKA_TEST_FIXTURE_ONLY") == "set"

def test_z_fixture_restored():
    assert "ORKA_TEST_FIXTURE_ONLY" not in os.environ
''')
            result = subprocess.run([sys.executable, str(Path(__file__).with_name('gui_contracts.py')), '--gui-root', root], capture_output=True, text=True)
            self.assertEqual(result.returncode, 1)
            self.assertIn('Ran 4 tests', result.stderr)
            self.assertIn('function contract was actually executed', result.stderr)
