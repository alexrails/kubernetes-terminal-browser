#!/usr/bin/env python3
"""Local PTY smoke test; uses only a fake kubectl, never a real cluster."""
import os
import pty
import fcntl
import select
import signal
import struct
import subprocess
import tempfile
import termios
import time
from pathlib import Path


FAKE = r'''#!/usr/bin/env python3
import json, sys, signal, os, time
args=sys.argv[1:]
if 'version' in args:
    print(json.dumps({'clientVersion': {'gitVersion': 'v1.35.0'}}))
elif 'config' in args and 'view' in args:
    name='test'
    print(json.dumps({'current-context':name,'contexts':[{'name':name,'context':{'namespace':'default','user':'test'}}], 'users':[{'name':'test','user':{}}]}))
elif 'get' in args and 'namespaces' in args:
    print(json.dumps({'items':[{'metadata':{'name':'default'}},{'metadata':{'name':'team-b'}}]}))
elif 'get' in args and 'pods' in args:
    print(json.dumps({'items':[{'metadata':{'name':'one','uid':'uid-1'},'spec':{'containers':[{'name':'app'}]},'status':{'phase':'Running','containerStatuses':[{'name':'app','state':{'running':{}}}]}}]}))
elif 'get' in args and 'pod' in args:
    print(json.dumps({'metadata':{'name':'one','uid':'uid-1'},'spec':{'containers':[{'name':'app'}]},'status':{'phase':'Running','containerStatuses':[{'name':'app','state':{'running':{}}}]}}))
elif 'exec' in args:
    if not all(os.isatty(fd) for fd in (0, 1, 2)):
        print('EXEC_TTY_ERROR:'+','.join(str(os.isatty(fd)) for fd in (0, 1, 2)), file=sys.stderr, flush=True)
        sys.exit(1)
    signal.signal(signal.SIGINT, lambda *_: sys.exit(130))
    print('EXEC_READY', flush=True)
    line=sys.stdin.readline()
    print('EXEC_DONE:'+line.strip(), flush=True)
elif 'logs' in args:
    with open(os.environ['KTB_LOG_PIDFILE'],'w') as f:f.write(str(os.getpid()))
    print('LOG_START',flush=True)
    while True:time.sleep(1)
else:
    print('unexpected: '+repr(args),file=sys.stderr)
    sys.exit(1)
'''

FAKE_GCLOUD = r'''#!/usr/bin/env python3
import os, sys
want=['container','clusters','get-credentials','test-cluster','--region','europe-west2','--project','acme-test']
if sys.argv[1:] != want:
    print('GCLOUD_ARGS_ERROR:'+repr(sys.argv[1:]),file=sys.stderr,flush=True)
    sys.exit(1)
if not all(os.isatty(fd) for fd in (0,1,2)):
    print('GCLOUD_TTY_ERROR:'+','.join(str(os.isatty(fd)) for fd in (0,1,2)),file=sys.stderr,flush=True)
    sys.exit(1)
print('GCLOUD_READY',flush=True)
'''


def main():
    binary = Path(__file__).resolve().parents[1] / "bin/ktb"
    if not binary.exists():
        raise SystemExit("build first: make build")
    with tempfile.TemporaryDirectory() as tmp:
        fake = Path(tmp) / "kubectl"
        fake_gcloud = Path(tmp) / "gcloud"
        config = Path(tmp) / "config.yaml"
        clusters = Path(tmp) / "clusters.yaml"
        log_pidfile = Path(tmp) / "log.pid"
        fake.write_text(FAKE)
        fake.chmod(0o700)
        fake_gcloud.write_text(FAKE_GCLOUD)
        fake_gcloud.chmod(0o700)
        config.write_text("""refresh: 5s
timeout: 15s
""")
        clusters.write_text("""clusters:
  - name: test-cluster
    region: europe-west2
    project: acme-test
""")
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 120, 0, 0))
        before = termios.tcgetattr(slave)
        proc = subprocess.Popen(
            [str(binary), "--kubectl", str(fake), "--gcloud", str(fake_gcloud), "--config", str(config), "--clusters", str(clusters), "--refresh", "0"],
            stdin=slave, stdout=slave, stderr=slave,
            env={**os.environ,"KTB_LOG_PIDFILE":str(log_pidfile)},
            start_new_session=True, preexec_fn=lambda: fcntl.ioctl(slave, termios.TIOCSCTTY, 0),
        )
        output = bytearray()

        def wait_for(needle, timeout=8):
            deadline = time.monotonic() + timeout
            while time.monotonic() < deadline:
                ready, _, _ = select.select([master], [], [], 0.05)
                if ready:
                    try:
                        output.extend(os.read(master, 65536))
                    except OSError:
                        break
                if needle.encode() in output:
                    return
            raise AssertionError(f"missing {needle!r}; last output {output[-1200:]!r}")

        try:
            wait_for("acme-test")
            os.write(master, b"\r")
            wait_for("GCLOUD_READY")
            wait_for("team-b")
            os.write(master, b"\r")
            wait_for("one")
            os.write(master, b"\r")
            wait_for("Rails console")
            output.clear()
            os.write(master, b"\x1b")
            wait_for("ctrl+s")  # popup closed: the applications footer is back
            os.write(master, b"\x13")  # ctrl+s: shell
            wait_for("EXEC_READY")
            os.write(master, b"done\n")
            wait_for("EXEC_DONE:done")
            wait_for("Session ended")
            output.clear()
            os.write(master, b"\x13")
            wait_for("EXEC_READY")
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 130, 0, 0))
            os.write(master, b"\x03")
            wait_for("exit 130")
            output.clear()
            os.write(master,b"\x0c")  # ctrl+l: logs
            wait_for("OG_START")  # Bubble Tea may preserve the initial "L" cell.
            log_pid=int(log_pidfile.read_text())
            os.write(master,b"\x1b")
            wait_for("ctrl+s")  # single container: Esc returns to the applications
            time.sleep(0.7)
            try:
                os.kill(log_pid,0)
            except ProcessLookupError:
                pass
            else:
                raise AssertionError("log follower survived viewer close")
            output.clear()
            os.write(master, b"\x13")
            wait_for("EXEC_READY")
            os.kill(proc.pid, signal.SIGTERM)
            deadline=time.monotonic()+3
            while proc.poll() is None and time.monotonic()<deadline:
                if select.select([master], [], [], 0.05)[0]:
                    try:
                        chunk=os.read(master,65536)
                        if chunk:
                            output.extend(chunk)
                    except OSError:
                        pass
            if proc.poll() is None:
                raise AssertionError(f"SIGTERM did not stop app; output tail: {output[-1200:]!r}")
            try:
                after = termios.tcgetattr(slave)
                if before != after:
                    raise AssertionError("terminal modes changed after SIGTERM")
            except termios.error:
                # macOS revokes the PTY's slave ioctl after its session leader exits.
                if output.count(b"\x1b[?1049l") < 2:
                    raise AssertionError("terminal cleanup sequence missing")
            print("PTY smoke passed: cluster selection, namespace choice, pod actions, exec, Ctrl+C, resize, log cancellation, SIGTERM, terminal modes")
        finally:
            if proc.poll() is None:
                proc.kill()
                proc.wait(timeout=3)
            os.close(master)
            os.close(slave)


if __name__ == "__main__":
    main()
