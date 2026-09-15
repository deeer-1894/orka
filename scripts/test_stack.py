"""Lifecycle contracts use temporary HTTP servers, never the running Orka stack."""
import importlib.util
import json
import os
from pathlib import Path
import socket
import signal
import time
import subprocess
import sys
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("orka_stack", Path(__file__).with_name("stack.py"))
stack = importlib.util.module_from_spec(spec)
spec.loader.exec_module(stack)


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


class ProcessContracts(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.manager = stack.ProcessManager(self.root)

    def tearDown(self):
        self.manager.stop("fixture")
        self.tmp.cleanup()

    def test_owned_process_binds_expected_port_and_stops(self):
        port = free_port()
        command = [sys.executable, "-m", "http.server", str(port), "--bind", "127.0.0.1"]
        pid = self.manager.start("fixture", command, port, self.root)
        self.assertTrue(self.manager.status("fixture")["running"])
        self.assertEqual(self.manager.start("fixture", command, port, self.root), pid)
        self.manager.stop("fixture")
        self.assertFalse(self.manager.status("fixture")["running"])

    def test_start_records_final_identity_after_delayed_exec(self):
        port = free_port()
        # Popen's first executable can be a launcher/interpreter in transition.
        # The stored identity must belong to the final listener, not that stage.
        command = [sys.executable, '-c',
                   'import os,sys,time; time.sleep(.15); os.execv(sys.executable, [sys.executable, "-m", "http.server", sys.argv[1], "--bind", "127.0.0.1"])',
                   str(port)]
        pid = self.manager.start('fixture', command, port, self.root)
        try:
            record = self.manager.record('fixture')
            actual = stack.process_identity(pid)
            self.assertEqual({key: record[key] for key in actual}, actual)
            # Another launcher invocation must accept and stop the persisted ID.
            restarted_manager = stack.ProcessManager(self.root)
            self.assertTrue(restarted_manager.status('fixture')['listening'])
            restarted_manager.stop('fixture')
            self.assertIsNone(stack.process_identity(pid))
        finally:
            # Keep a failing regression from leaking its exact Popen child.
            child = self.manager.children.get('fixture')
            if child is not None and child.poll() is None:
                child.kill()
                child.wait(timeout=3)

    def test_foreign_port_is_not_stopped_or_adopted(self):
        port = free_port()
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", port))
            listener.listen()
            with self.assertRaisesRegex(RuntimeError, "port"):
                self.manager.start("fixture", [sys.executable, "-c", "pass"], port, self.root)
            self.assertEqual(listener.getsockname()[1], port)

    def test_time_wait_allows_restart_but_live_reusable_listener_does_not(self):
        # Force the server side into TIME_WAIT: it actively closes first.
        with socket.socket() as listener:
            listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            listener.bind(("127.0.0.1", 0))
            listener.listen()
            port = listener.getsockname()[1]
            with socket.create_connection(("127.0.0.1", port)) as peer:
                accepted, _ = listener.accept()
                accepted.close()
                self.assertEqual(peer.recv(1), b"")
        deadline = time.monotonic() + 2
        while time.monotonic() < deadline:
            rows = Path('/proc/net/tcp').read_text().splitlines()[1:]
            if any(row.split()[3] == '06' and int(row.split()[1].split(':')[1], 16) == port for row in rows):
                break
            time.sleep(.01)
        else:
            self.fail("fixture did not create TIME_WAIT")
        self.assertTrue(stack.port_available(port), "TIME_WAIT was mistaken for a live service")
        pid = self.manager.start("fixture", [sys.executable, "-m", "http.server", str(port), "--bind", "127.0.0.1"], port, self.root)
        self.assertGreater(pid, 0)
        self.assertFalse(stack.port_available(port), "live reusable listener was accepted")

    def test_stop_waits_for_children_after_leader_exits(self):
        script = self.root / 'fork_fixture.py'
        script.write_text("""import os,signal,socket,sys,time
port=int(sys.argv[1])
pid=os.fork()
if pid == 0:
    signal.signal(signal.SIGTERM,signal.SIG_IGN)
    with open('child.pid','w') as f: f.write(str(os.getpid()))
    while True: time.sleep(1)
else:
    while not os.path.exists('child.pid'): time.sleep(.01)
    listener=socket.socket();listener.bind(('127.0.0.1',port));listener.listen()
    while True: time.sleep(1)
""")
        port = free_port()
        self.manager.start("fixture", [sys.executable, str(script), str(port)], port, self.root)
        child = int((self.root / 'child.pid').read_text())
        try:
            self.manager.stop("fixture")
            self.assertIsNone(stack.process_identity(child), "stop returned with an owned child still alive")
        finally:
            if stack.process_identity(child) is not None:
                os.kill(child, signal.SIGKILL)

    def test_reused_pid_metadata_cannot_kill_foreign_process(self):
        foreign = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(30)"], start_new_session=True)
        try:
            record = stack.process_identity(foreign.pid)
            record["start_ticks"] = "stale"
            record.update({"port": free_port(), "project": str(self.root)})
            self.manager.record_path("fixture").write_text(json.dumps(record))
            with self.assertRaisesRegex(RuntimeError, "identity"):
                self.manager.stop("fixture")
            self.assertIsNone(foreign.poll())
            self.manager.record_path("fixture").unlink()
        finally:
            foreign.terminate()
            foreign.wait(timeout=5)

    def test_failed_start_cleans_only_its_own_process(self):
        with self.assertRaises(RuntimeError):
            self.manager.start("fixture", [sys.executable, "-c", "raise SystemExit(3)"], free_port(), self.root)
        self.assertFalse(self.manager.status("fixture")["running"])

class PortConfigurationContracts(unittest.TestCase):
    def test_tools_default_matches_repository_configuration(self):
        from unittest.mock import patch
        with patch.dict(os.environ, {}, clear=True):
            self.assertEqual(stack.component_port('tools'), 8090)

    def test_explicit_port_is_validated(self):
        from unittest.mock import patch
        with patch.dict(os.environ, {'CONTROL_ADDR': ':18188'}):
            self.assertEqual(stack.component_port('control'), 18188)
        with patch.dict(os.environ, {'WEB_PORT': '99999'}):
            with self.assertRaises(ValueError):
                stack.component_port('web')


if __name__ == "__main__":
    unittest.main()
