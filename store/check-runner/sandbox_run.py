"""C8 (story 54.16): every stage of the package through the harness (`mkp test`, the host's
own code) on the plugin's fixtures and on generated events; prints C8's JSON. Blocks on a
trap on the plugin's own fixtures, or a peak memory over 80 % of what the manifest declares."""
import glob
import json
import os
import subprocess
import sys
import tomllib

pkg, fixtures_dir, generated = sys.argv[1], sys.argv[2], sys.argv[3]
manifest = tomllib.load(open(os.path.join(pkg, "manifest.toml"), "rb"))
memory_mb = manifest.get("resources", {}).get("memory_mb", 256)
own = sorted(glob.glob(os.path.join(fixtures_dir, "*.json"))) if os.path.isdir(fixtures_dir) else []
runs = [(f, True) for f in own] + [(generated, False)]
peak = 0
calls = {}
for stage in manifest.get("stage", []):
    for fixtures, is_own in runs:
        p = subprocess.run(["mkp", "test", pkg, "--stage", stage["name"], "--fixtures", fixtures], capture_output=True, text=True, timeout=600)
        try:
            out = json.loads(p.stdout)
        except json.JSONDecodeError:
            print(json.dumps({"result": "block", "code": "plugin_harness_failed", "detail": (p.stderr or p.stdout)[-2000:]}))
            sys.exit(0)
        r = out.get("result", {})
        peak = max(peak, r.get("peak_memory", 0))
        for c in out.get("calls", []):
            calls[c.get("call", "?")] = calls.get(c.get("call", "?"), 0) + 1
        if is_own and r.get("outcome") in ("trap", "timeout", "memory"):
            detail = f"{stage['name']} on {os.path.basename(fixtures)}: {r.get('outcome')} {r.get('error_code', '')}\n{r.get('backtrace', r.get('message', ''))}"
            print(json.dumps({"result": "block", "code": f"plugin_{r.get('outcome')}", "detail": detail[-4000:], "peak_memory_mb": peak >> 20}))
            sys.exit(0)
if peak > memory_mb * 0.8 * (1 << 20):
    print(json.dumps({"result": "block", "code": "memory_over_80", "detail": f"peak {peak >> 20} MB of {memory_mb} MB declared", "peak_memory_mb": peak >> 20}))
    sys.exit(0)
print(json.dumps({"result": "pass", "peak_memory_mb": peak >> 20, "calls": calls, "generated_only": not own,
                  "detail": "the plugin's fixtures are empty: generated events only" if not own else ""}))
