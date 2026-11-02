#!/usr/bin/env python3
"""Prints the UI test classes of one CI shard, one a line (.github/workflows/_apple-apps.yml).

    ui-shard.py SHARD SHARDS FILE...

Each file is a test class named after it. Classes go heaviest first (by `func test` count)
to the shard with the fewest tests so far, so the shards stay even as classes are added, and
every class is in exactly one shard.
"""
import os
import re
import sys


def shards(files: list[str], count: int) -> list[list[str]]:
    weight = {}
    for f in files:
        with open(f, encoding="utf-8") as fh:
            weight[os.path.basename(f).removesuffix(".swift")] = len(re.findall(r"func test", fh.read()))
    load, out = [0] * count, [[] for _ in range(count)]
    for name in sorted(weight, key=lambda n: (-weight[n], n)):
        i = min(range(count), key=lambda k: (load[k], k))
        load[i] += weight[name]
        out[i].append(name)
    return out


if __name__ == "__main__":
    shard, count = int(sys.argv[1]), int(sys.argv[2])
    print("\n".join(shards(sys.argv[3:], count)[shard - 1]))
