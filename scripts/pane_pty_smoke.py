#!/usr/bin/env python3
"""PTY smoke test for the menu's pane entries: a fake multiplexer records the pane command, which then runs in a second PTY."""
import base64
import fcntl
import json
import os
import pty
import select
import struct
import subprocess
import tempfile
import termios
import time
from pathlib import Path

from pty_smoke import FAKE, FAKE_GCLOUD

MUX = r'''#!/bin/sh
printf '%s\n' "$@" > "$KTB_PANE_ARGS"
'''

# Records the directory and kubeconfig an exec would use, then behaves like the fake kubectl.
KUBECTL = r'''#!/bin/sh
case " $* " in *" exec "*) printf '%s|%s' "$(pwd -P)" "${KUBECONFIG-unset}" > "$KTB_SEEN_KUBECONFIG";; esac
exec "$(dirname "$0")/kubectl.py" "$@"
'''


class Term:
    def __init__(self, argv, env, cwd=None):
        self.master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 120, 0, 0))
        self.proc = subprocess.Popen(argv, stdin=slave, stdout=slave, stderr=slave, env=env, cwd=cwd,
                                     start_new_session=True, preexec_fn=lambda: fcntl.ioctl(slave, termios.TIOCSCTTY, 0))
        os.close(slave)
        self.output = bytearray()

    def read(self):
        if select.select([self.master], [], [], 0.05)[0]:
            try:
                self.output.extend(os.read(self.master, 65536))
            except OSError:
                pass

    def wait_for(self, needle, timeout=8):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            self.read()
            if needle.encode() in self.output:
                return
        raise AssertionError(f"missing {needle!r}; last output {self.output[-1200:]!r}")

    def send(self, data):
        os.write(self.master, data)

    def wait_exit(self, timeout=5):
        deadline = time.monotonic() + timeout
        while self.proc.poll() is None and time.monotonic() < deadline:
            self.read()
        if self.proc.poll() is None:
            raise AssertionError(f"still running; last output {self.output[-1200:]!r}")
        return self.proc.returncode

    def close(self):
        # macOS keeps an exiting process until its PTY output is read.
        if self.proc.poll() is None:
            self.proc.kill()
            self.wait_exit()
        os.close(self.master)


def main():
    binary = Path(__file__).resolve().parents[1] / "bin/ktb"
    if not binary.exists():
        raise SystemExit("build first: make build")
    with tempfile.TemporaryDirectory() as tmp:
        tmp = Path(tmp)
        for name, body in (("kubectl", KUBECTL), ("kubectl.py", FAKE), ("gcloud", FAKE_GCLOUD), ("mux", MUX)):
            (tmp / name).write_text(body)
            (tmp / name).chmod(0o700)
        args_file = tmp / "pane.args"
        (tmp / "config.yaml").write_text(f"refresh: 5s\ntimeout: 15s\npane: ['{tmp / 'mux'}', split]\n")
        (tmp / "clusters.yaml").write_text("clusters:\n  - name: test-cluster\n    region: europe-west2\n    project: acme-test\n")
        seen = tmp / "kubeconfig.seen"
        env = {**os.environ, "KTB_PANE_ARGS": str(args_file), "KTB_SEEN_KUBECONFIG": str(seen), "KUBECONFIG": "./selected.yaml:/opened/b"}
        # A pane starts in the multiplexer server's environment and directory, not in the TUI's.
        server = {**env, "KUBECONFIG": "/server/config"}
        want = f"{os.path.realpath(tmp)}|./selected.yaml:/opened/b"
        tui = Term([str(binary), "--kubectl", str(tmp / "kubectl"), "--gcloud", str(tmp / "gcloud"), "--config", str(tmp / "config.yaml"),
                    "--clusters", str(tmp / "clusters.yaml"), "--refresh", "0"], env, cwd=tmp)
        terms = [tui]
        try:
            tui.wait_for("acme-test")
            tui.send(b"\r")
            tui.wait_for("team-b")
            tui.send(b"\r")
            tui.wait_for("one")
            tui.send(b"\r")
            tui.wait_for("in a new pane")
            tui.send(b"3")  # the first action, in a new pane
            tui.wait_for("Opened Shell")
            if b"EXEC_READY" in tui.output:
                raise AssertionError("the action ran in the TUI's own terminal")
            argv = args_file.read_text().splitlines()
            if argv[0] != "split" or argv[1] != str(binary) or argv[2] != "--pane-session" or len(argv) != 4:
                raise AssertionError(f"pane command: {argv!r}")

            pane = Term(argv[1:], server, cwd="/")
            terms.append(pane)
            pane.wait_for("this pane closes when the command exits")
            pane.wait_for("EXEC_READY")
            if seen.read_text() != want:
                raise AssertionError(f"pane exec ran in {seen.read_text()!r}, want {want!r}")
            pane.send(b"done\n")
            pane.wait_for("EXEC_DONE:done")
            if pane.wait_exit() != 0:
                raise AssertionError(f"pane exit {pane.proc.returncode}")

            # The TUI is still alive beside the pane and still answers keys.
            tui.output.clear()
            tui.send(b"\r")
            tui.wait_for("Rails console")

            # A pod replaced under the same name is refused, and the message waits for Enter.
            session = json.loads(base64.urlsafe_b64decode(argv[3] + "=" * (-len(argv[3]) % 4)))
            session["Ref"]["Pod"]["UID"] = "uid-replaced"
            stale = base64.urlsafe_b64encode(json.dumps(session).encode()).decode().rstrip("=")
            # herdr's way in: the session in the environment, not on the command line.
            pane = Term([str(binary), "--pane-session"], {**server, "KTB_PANE_SESSION": stale})
            terms.append(pane)
            pane.wait_for("Pod identity changed")
            pane.wait_for("Press Enter to close this pane")
            if b"EXEC_READY" in pane.output:
                raise AssertionError("exec ran on a replaced pod")
            time.sleep(0.3)
            if pane.proc.poll() is not None:
                raise AssertionError("pane closed before the error could be read")
            pane.send(b"\n")
            if pane.wait_exit() != 0:
                raise AssertionError(f"pane exit {pane.proc.returncode}")
            tui.send(b"\x03")
            if tui.wait_exit() != 0:
                raise AssertionError(f"TUI exit {tui.proc.returncode}")
            print("Pane PTY smoke passed: the menu hands the session to the opener, the pane execs with a TTY and the opener's KUBECONFIG and directory and exits, the TUI stays, a replaced pod is refused")
        finally:
            for t in terms:
                t.close()


if __name__ == "__main__":
    main()
