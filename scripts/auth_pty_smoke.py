#!/usr/bin/env python3
"""Fake-kubectl PTY contract for Always and IfAvailable credential modes."""
import fcntl
import os
import pty
import select
import signal
import struct
import subprocess
import tempfile
import termios
import time
from pathlib import Path

FAKE=r'''#!/usr/bin/env python3
import json, os, sys
args=sys.argv[1:]
if 'version' in args:
    print(json.dumps({'clientVersion':{'gitVersion':'v1.36.1'}}))
elif 'config' in args and 'view' in args:
    print(json.dumps({'current-context':'auth-test','contexts':[{'name':'auth-test','context':{'namespace':'default','user':'auth-test'}}],'users':[{'name':'auth-test','user':{'exec':{'interactiveMode':os.environ['KTB_MODE']}}}]}))
elif 'get' in args and 'pods' in args:
    with open(os.environ['KTB_CALLS'],'a') as f:f.write('foreground\n' if sys.stdin.isatty() else 'background\n')
    if sys.stdin.isatty():
        print('AUTH_FOREGROUND',file=sys.stderr,flush=True)
        print(json.dumps({'items':[{'metadata':{'name':'authenticated','uid':'uid'},'spec':{'containers':[{'name':'app'}]},'status':{'phase':'Running'}}]}))
    else:
        print('getting credentials: exec plugin requires a terminal',file=sys.stderr)
        sys.exit(1)
elif 'get' in args and 'pod' in args:
    print(json.dumps({'metadata':{'name':'authenticated','uid':'uid'},'spec':{'containers':[{'name':'app'}]},'status':{'phase':'Running','containerStatuses':[{'name':'app','state':{'running':{}}}]}}))
elif 'logs' in args:
    terminal_fds=all(os.isatty(fd) for fd in (0,1,2))
    print('LOG_FOREGROUND' if terminal_fds else 'LOG_TTY_ERROR:'+','.join(str(os.isatty(fd)) for fd in (0,1,2)),flush=True)
    if not terminal_fds:sys.exit(1)
else:
    print('unexpected '+repr(args),file=sys.stderr)
    sys.exit(1)
'''

def run_mode(mode,binary,fake,tmp):
    calls=Path(tmp)/f"{mode}.calls"
    master,slave=pty.openpty()
    fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack("HHHH",30,120,0,0))
    env={**os.environ,'KTB_MODE':mode,'KTB_CALLS':str(calls),'XDG_CONFIG_HOME':str(tmp)}  # ignore the user's ktb config
    proc=subprocess.Popen([str(binary),'--kubectl',str(fake),'--refresh','1s'],stdin=slave,stdout=slave,stderr=slave,env=env,start_new_session=True,preexec_fn=lambda:fcntl.ioctl(slave,termios.TIOCSCTTY,0))
    output=bytearray()
    def wait_for(needle,timeout=8):
        deadline=time.monotonic()+timeout
        while time.monotonic()<deadline:
            if select.select([master],[],[],0.05)[0]:
                try:
                    chunk=os.read(master,65536)
                    if chunk:output.extend(chunk)
                except OSError:break
            if needle.encode() in output:return
        raise AssertionError(f"{mode}: missing {needle}; tail={output[-1500:]!r}")
    try:
        wait_for('auth-test')
        os.write(master,b'\r')
        if mode=='Always':
            wait_for('ManualOnly')
            time.sleep(1.2)
            if calls.exists():raise AssertionError('Always made a background API request')
        else:
            wait_for('NeedsAuth')
            if calls.read_text().splitlines()!=['background']:raise AssertionError(calls.read_text())
        os.write(master,b'\x12')  # ctrl+r
        wait_for('AUTH_FOREGROUND')
        wait_for('authenticated')
        if mode=='IfAvailable':wait_for('ManualOnly')
        time.sleep(1.2)
        expected=['foreground'] if mode=='Always' else ['background','foreground','background']
        if calls.read_text().splitlines()!=expected:raise AssertionError(f"{mode}: calls={calls.read_text()!r}")
        output.clear()
        if mode=='Always':
            # Manual logs: ctrl+l opens the options screen without a request, r hands
            # the terminal over, Enter after kubectl exits returns to the same screen.
            os.write(master,b'\x0c')
            wait_for('Manual logs')
            os.write(master,b'r')
            wait_for('LOG_FOREGROUND')
            wait_for('Press Enter')
            os.write(master,b'\n')
            wait_for('Session ended')
            time.sleep(0.6)
            if calls.read_text().splitlines()!=expected:raise AssertionError(f"{mode}: logs made a pod list request: calls={calls.read_text()!r}")
        else:
            # ManualOnly sticks: another manual read is foreground only, no new probe.
            os.write(master,b'\x12')
            wait_for('AUTH_FOREGROUND')
            wait_for('ManualOnly')
            time.sleep(1.2)
            if calls.read_text().splitlines()!=expected+['foreground']:raise AssertionError(f"{mode}: ManualOnly did not stick: calls={calls.read_text()!r}")
        os.write(master,b'\x03')  # ctrl+c
        deadline=time.monotonic()+3
        while proc.poll() is None and time.monotonic()<deadline:
            if select.select([master],[],[],0.05)[0]:
                try:os.read(master,65536)
                except OSError:pass
        if proc.poll() is None:raise AssertionError(f"{mode}: did not quit")
    finally:
        if proc.poll() is None:
            proc.kill();proc.wait(timeout=3)
        os.close(master);os.close(slave)

def main():
    binary=Path(__file__).resolve().parents[1]/'bin/ktb'
    if not binary.exists():raise SystemExit('build first: make build')
    with tempfile.TemporaryDirectory() as tmp:
        fake=Path(tmp)/'kubectl';fake.write_text(FAKE);fake.chmod(0o700)
        for mode in ('Always','IfAvailable'):run_mode(mode,binary,fake,tmp)
    print('auth PTY passed: Always manual incl. foreground logs; IfAvailable one background probe, then ManualOnly sticks')

if __name__=='__main__':main()
