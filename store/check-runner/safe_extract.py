"""Extracts a tar from stdin into a folder, refusing anything but regular files and folders
inside it (story 54.16: the runner reads untrusted archives). Python's own "data" filter, and
a size cap of 250 MB."""
import sys
import tarfile

MAX = 250 << 20
dest = sys.argv[1]
total = 0
with tarfile.open(fileobj=sys.stdin.buffer, mode="r|*") as tar:
    for m in tar:
        if not (m.isfile() or m.isdir()):
            sys.exit(f"refused entry {m.name!r}: not a file")
        total += m.size
        if total > MAX:
            sys.exit("the archive is too large")
        tar.extract(m, dest, filter="data")
