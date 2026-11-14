"""Runs a command under a pseudo-terminal and relays it (story 58.12 T03).

brs-engine's CLI saves its screen as a PNG on Ctrl+S, which it reads only from a terminal:
this gives it one. The command's output goes to stdout as it comes; bytes written to stdin go
to the command's terminal. It ends when the command ends or stdin closes.

    python3 pty_run.py node brs.cli.js app.zip --ecp -s shot.png
"""

import os
import pty
import select
import signal
import sys


def main() -> int:
    pid, fd = pty.fork()
    if pid == 0:
        os.execvp(sys.argv[1], sys.argv[1:])
    stdin = sys.stdin.fileno()
    open_in = True
    while True:
        watch = [fd] + ([stdin] if open_in else [])
        try:
            ready, _, _ = select.select(watch, [], [], 0.2)
        except InterruptedError:
            continue
        if fd in ready:
            try:
                data = os.read(fd, 65536)
            except OSError:
                break
            if not data:
                break
            sys.stdout.buffer.write(data)
            sys.stdout.buffer.flush()
        if open_in and stdin in ready:
            data = os.read(stdin, 1024)
            if not data:
                os.kill(pid, signal.SIGTERM)
                open_in = False
            else:
                os.write(fd, data)
        done, _ = os.waitpid(pid, os.WNOHANG)
        if done:
            break
    try:
        os.kill(pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    return 0


if __name__ == "__main__":
    sys.exit(main())
