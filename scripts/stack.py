#!/usr/bin/env python3
"""Linux project-scoped launcher. No model probes, name-based kills, or data deletion."""
from contextlib import contextmanager
import fcntl
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import sys
import time


def process_identity(pid):
    try:
        proc = Path('/proc') / str(pid)
        fields = (proc / 'stat').read_text().rsplit(')', 1)[1].split()
        if fields[0] == 'Z':
            return None
        return {'pid': pid, 'start_ticks': fields[19], 'group': int(fields[2]),
                'session': int(fields[3]), 'exe': os.readlink(proc / 'exe'),
                'argv_hash': hashlib.sha256((proc / 'cmdline').read_bytes()).hexdigest()}
    except (FileNotFoundError, ProcessLookupError, PermissionError):
        return None


def port_available(port):
    with socket.socket() as listener:
        # Match Go/Vite listeners: TIME_WAIT can be reused, a live listener cannot.
        listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        try:
            listener.bind(('127.0.0.1', port))
            return True
        except OSError:
            return False


def group_members(group):
    members = {}
    for proc in Path('/proc').iterdir():
        if proc.name.isdigit():
            identity = process_identity(int(proc.name))
            if identity and identity['group'] == group and identity['session'] == group:
                members[identity['pid']] = identity
    return members


def same_process(identity):
    current = process_identity(identity['pid'])
    return bool(current and all(current[key] == identity[key]
                                for key in ('pid', 'start_ticks', 'group', 'session')))


def signal_process(identity, sig):
    # A pidfd pins the signal recipient even if a PID exits/recycles during stop.
    # Executable/argv may legitimately change when a child execs during shutdown.
    try:
        fd = os.pidfd_open(identity['pid'])
    except ProcessLookupError:
        return
    try:
        if same_process(identity):
            try:
                signal.pidfd_send_signal(fd, sig)
            except ProcessLookupError:
                pass
    finally:
        os.close(fd)


def group_owns_port(group, port):
    inodes = set()
    for table in ('/proc/net/tcp', '/proc/net/tcp6'):
        for line in Path(table).read_text().splitlines()[1:]:
            fields = line.split()
            if fields[3] == '0A' and int(fields[1].rsplit(':', 1)[1], 16) == port:
                inodes.add('socket:[' + fields[9] + ']')
    for proc in Path('/proc').iterdir():
        if not proc.name.isdigit():
            continue
        try:
            stat = (proc / 'stat').read_text().rsplit(')', 1)[1].split()
            if int(stat[2]) != group:
                continue
            for fd in (proc / 'fd').iterdir():
                try:
                    if os.readlink(fd) in inodes:
                        return True
                except (FileNotFoundError, PermissionError):
                    pass
        except (FileNotFoundError, ProcessLookupError, PermissionError):
            pass
    return False


class ProcessManager:
    def __init__(self, root):
        if not Path('/proc/self/stat').exists():
            raise RuntimeError('project launcher requires Linux /proc process identity checks')
        self.root = Path(root).resolve()
        self.run_dir = self.root / '.run'
        self.run_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
        self.children = {}

    def record_path(self, name):
        if not name.replace('_', '').isalnum():
            raise ValueError('invalid component name')
        return self.run_dir / (name + '.json')

    @contextmanager
    def locked(self):
        with (self.run_dir / 'stack.lock').open('a') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            yield

    def record(self, name):
        try:
            return json.loads(self.record_path(name).read_text())
        except FileNotFoundError:
            return None

    def verified(self, record):
        current = process_identity(record['pid'])
        if current is None:
            return False
        if record.get('project') != str(self.root) or any(record.get(k) != current[k] for k in current):
            raise RuntimeError('process identity mismatch; refusing to signal or adopt it')
        if current['group'] != current['pid'] or current['session'] != current['pid']:
            raise RuntimeError('process identity is not an owned session leader')
        return True

    def status(self, name):
        record = self.record(name)
        running = bool(record and self.verified(record))
        return {'component': name, 'running': running,
                'pid': record['pid'] if running else None,
                'port': record['port'] if record else None,
                'listening': bool(running and group_owns_port(record['pid'], record['port']))}

    def start(self, name, command, port, cwd, env=None):
        with self.locked():
            record = self.record(name)
            if record and self.verified(record):
                if record['port'] != port or not group_owns_port(record['pid'], port):
                    raise RuntimeError(f'{name} is running but does not own expected port {port}')
                return record['pid']
            if not port_available(port):
                raise RuntimeError(f'port {port} is occupied by an unmanaged process; refusing to stop it')
            with (self.run_dir / (name + '.log')).open('ab') as log:
                child = subprocess.Popen(command, cwd=cwd, env=env, stdin=subprocess.DEVNULL,
                                         stdout=log, stderr=log, start_new_session=True)
            self.children[name] = child
            birth = None
            candidate = None
            deadline = time.monotonic() + 15
            try:
                while time.monotonic() < deadline:
                    if child.poll() is not None:
                        raise RuntimeError(f'{name} exited during startup; see .run/{name}.log')
                    current = process_identity(child.pid)
                    if current is not None:
                        if birth is None:
                            birth = current
                        if not same_process(birth):
                            raise RuntimeError('startup process identity changed')
                        if current['group'] != child.pid or current['session'] != child.pid:
                            raise RuntimeError('startup process is not its session leader')
                        # Popen returns before every interpreter/launcher has
                        # finished exec/argv initialization. Commit only the final
                        # listening identity, stable across two readiness samples.
                        if group_owns_port(child.pid, port):
                            if current == candidate:
                                record = dict(current, project=str(self.root), port=port)
                                temp = self.record_path(name).with_suffix('.tmp')
                                temp.write_text(json.dumps(record))
                                temp.replace(self.record_path(name))
                                return child.pid
                            candidate = current
                        else:
                            candidate = None
                    else:
                        candidate = None
                    time.sleep(0.05)
                raise RuntimeError(f'{name} did not bind expected port {port}; see .run/{name}.log')
            except BaseException:
                # No steady-state record exists yet. This Popen child is still
                # ours across exec; its birth identity, not transient argv, owns
                # cleanup. PID/start/session verification remains mandatory.
                if birth is not None:
                    self._stop_group(birth)
                child.wait(timeout=3)
                self.children.pop(name, None)
                raise

    def stop(self, name):
        with self.locked():
            self._stop(name)

    def _stop_group(self, identity):
        members = group_members(identity['pid'])
        if members and not same_process(identity):
            raise RuntimeError('session leader is gone; cannot verify remaining processes; retaining ownership record')
        tracked = members
        for member in tracked.values():
            signal_process(member, signal.SIGTERM)
        deadline = time.monotonic() + 8
        kill_deadline = deadline + 3
        while tracked:
            alive = {pid: item for pid, item in tracked.items() if same_process(item)}
            if not alive:
                break
            current = group_members(identity['pid'])
            for pid, item in current.items():
                if pid not in tracked:
                    signal_process(item, signal.SIGTERM)
            tracked = current
            if time.monotonic() >= deadline:
                for member in tracked.values():
                    signal_process(member, signal.SIGKILL)
            if time.monotonic() >= kill_deadline:
                raise RuntimeError('owned processes have not exited; retaining ownership record')
            time.sleep(0.05)
        if group_members(identity['pid']):
            raise RuntimeError('process group is still occupied; retaining ownership record')

    def _stop(self, name):
        record = self.record(name)
        if record:
            # Persisted records must match the complete final exec identity.
            verified = self.verified(record)
            if verified:
                self._stop_group(record)
            elif group_members(record['pid']):
                raise RuntimeError('session leader is gone; cannot verify remaining processes; retaining ownership record')
        child = self.children.pop(name, None)
        if child is not None:
            child.wait(timeout=3)
        self.record_path(name).unlink(missing_ok=True)


def component_port(name):
    key, default = {'control': ('CONTROL_ADDR', ':8088'), 'tools': ('TOOLS_ADDR', ':8090'),
                    'web': ('WEB_PORT', '5173'), 'gui': ('GUI_PORT', '8100')}[name]
    port = int(os.getenv(key, default).rsplit(':', 1)[-1])
    if not 1 <= port <= 65535:
        raise ValueError(f'invalid {key} port')
    return port


def component(root, name):
    root = Path(root)
    if name in ('control', 'tools'):
        module = 'orka_control_layer' if name == 'control' else 'tools_server'
        binary = root / '.run' / ('orka_' + name)
        staged = binary.with_suffix('.next')
        version = subprocess.check_output(['git', 'rev-parse', '--short=12', 'HEAD'], cwd=root, text=True).strip()
        args = ['go', 'build']
        if name == 'control':
            stamp = time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())
            args += ['-ldflags', f'-X github.com/orka-oss/orka_control_layer/api.BuildVersion={version} -X github.com/orka-oss/orka_control_layer/api.BuildTime={stamp}']
        subprocess.run(args + ['-o', str(staged), './' + module], cwd=root, check=True)
        staged.replace(binary)
        return [str(binary)], component_port(name), root
    if name == 'web':
        node = shutil.which('node')
        vite = root / 'web/node_modules/vite/bin/vite.js'
        if not node or not vite.exists():
            raise RuntimeError('web needs Node and npm ci --prefix web before startup')
        port = component_port(name)
        return [node, str(vite), '--host', '127.0.0.1', '--port', str(port), '--strictPort'], port, root / 'web'
    if name == 'gui':
        python = os.getenv('GUI_PYTHON') or str(root / 'gui_agent/.venv/bin/python')
        if not Path(python).exists():
            raise RuntimeError('set GUI_PYTHON to an interpreter with gui_agent requirements and Chromium installed')
        return [python, 'main.py'], component_port(name), root / 'gui_agent'
    raise RuntimeError('unknown component: ' + name)


def main(argv):
    root = Path(__file__).resolve().parent.parent
    manager = ProcessManager(root)
    # Serialize build+start as well as stop, so another invocation cannot replace
    # a binary after its process identity was recorded.
    with (manager.run_dir / 'launcher.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        run_command(manager, root, argv)


def run_command(manager, root, argv):
    names = ('tools', 'gui', 'control', 'web')
    action = argv[0] if argv else 'all'
    if action == 'status':
        print(json.dumps([manager.status(name) for name in names], indent=2))
        return
    if action == 'stop':
        for name in reversed(names if len(argv) == 1 else (argv[1],)):
            if name not in names:
                raise RuntimeError('unknown component: ' + name)
            manager.stop(name)
        return
    targets = names if action == 'all' else (action,)
    started = []
    try:
        for name in targets:
            if name not in names:
                raise RuntimeError('use all | control | tools | gui | web | status | stop [component]')
            if manager.status(name)['running']:
                print(f'{name}: already running (use stop {name} before rebuilding)')
                continue
            expected_port = component_port(name)
            if not port_available(expected_port):
                raise RuntimeError(f'port {expected_port} is occupied by an unmanaged process; refusing to stop it')
            command, port, cwd = component(root, name)
            pid = manager.start(name, command, port, cwd)
            started.append(name)
            print(f'{name}: pid {pid} owns :{port}; log .run/{name}.log')
    except BaseException:
        for name in reversed(started):
            manager.stop(name)
        raise


if __name__ == '__main__':
    try:
        main(sys.argv[1:])
    except (RuntimeError, ValueError, OSError, subprocess.CalledProcessError) as error:
        print(f'orka launcher: {error}', file=sys.stderr)
        sys.exit(1)
