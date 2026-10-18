from pathlib import Path
import subprocess
import unittest


class MakeContracts(unittest.TestCase):
    def test_gui_target_uses_single_compose_configuration(self):
        root = Path(__file__).resolve().parent.parent
        result = subprocess.run(['make', '-n', 'gui-sandbox'], cwd=root, capture_output=True, text=True, check=True)
        self.assertIn('docker compose -f docker-compose.tools.yml up', result.stdout)
        self.assertNotIn('docker run', result.stdout)
        self.assertNotIn('-p 8100:8100', result.stdout)
