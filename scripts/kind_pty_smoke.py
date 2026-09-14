#!/usr/bin/env python3
"""PTY smoke against the disposable ktb-validation kind cluster."""
import fcntl
import os
import pty
import select
import signal
import struct
import subprocess
import termios
import time
from pathlib import Path


def main():
    root=Path(__file__).resolve().parents[1]
    binary=root/"bin/ktb"
    kubectl=root/".tools/kubectl"
    config=root/".cache/kind-kubeconfig"
    for path in (binary,kubectl,config):
        if not path.exists():
            raise SystemExit(f"missing {path}")
    master,slave=pty.openpty()
    fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack("HHHH",35,125,0,0))
    proc=subprocess.Popen([str(binary),"--kubectl",str(kubectl),"--kubeconfig",str(config),"--namespace","ktb-validation","--refresh","0"],env={**os.environ,"XDG_CONFIG_HOME":str(Path(__file__).resolve().parents[1]/".cache")},stdin=slave,stdout=slave,stderr=slave,start_new_session=True,preexec_fn=lambda:fcntl.ioctl(slave,termios.TIOCSCTTY,0))
    output=bytearray()
    def wait_for(needle,timeout=15):
        deadline=time.monotonic()+timeout
        while time.monotonic()<deadline:
            if select.select([master],[],[],0.05)[0]:
                try:
                    chunk=os.read(master,65536)
                    if chunk:output.extend(chunk)
                except OSError:break
            if needle.encode() in output:return
        raise AssertionError(f"missing {needle!r}; tail={output[-2000:]!r}")
    try:
        wait_for("Contexts")
        os.write(master,b"\r")
        wait_for("running")
        output.clear()
        os.write(master,b"running")  # typing filters the list
        wait_for("1 match")
        os.write(master,b"\x13")  # ctrl+s: shell
        wait_for("running / app",20)
        wait_for("/ # ",20)
        output.clear()
        os.write(master,b"printf 'LIVE_EXEC_OK\\n'\n")
        wait_for("LIVE_EXEC_OK",10)
        os.write(master,b"exit\n")
        wait_for("Session ended",10)
        output.clear()
        os.write(master,b"\x04")  # ctrl+d: describe
        wait_for("Name:             running",15)
        os.write(master,b"\x03")  # ctrl+c
        deadline=time.monotonic()+3
        while proc.poll() is None and time.monotonic()<deadline:
            if select.select([master],[],[],0.05)[0]:
                try:os.read(master,65536)
                except OSError:pass
        if proc.poll() is None:raise AssertionError("app did not quit")
        print("kind PTY passed: live pod selection, shell exec, return, describe")
    finally:
        if proc.poll() is None:
            proc.kill()
            proc.wait(timeout=3)
        os.close(master);os.close(slave)


if __name__=="__main__":main()
