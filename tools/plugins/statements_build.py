"""Build and check a first-party plugin's named statements (Epic 54, story 54.24 §1).

``sync <plugin>`` reads every SQL text the plugin's code passes to its database
(``fetch``, ``fetchrow``, ``fetchval``, ``execute``, ``executemany``) in its own package
and in the modules its ``statements.toml`` lists, and writes ``queries/<name>.sql`` for
each, with a header from the statement's parse tree: ``mode`` (read or write), the tables
it reads and writes, and its ``scope`` (a ``library_id = $n`` or ``video_id = $n`` test
the API checks against the job's token). Names already given are kept.

``check <plugin>|--all`` (CI) fails when ``queries/`` is not what ``sync`` would write,
when a statement touches a table outside the manifest's ``[trusted] db.read`` /
``db.write``, writes a queue table or ``videos.state`` (only the engine writes those,
README §15.3), or when the code builds a statement's text at run time (an f-string or a
``+`` of variables): such a statement can never be registered, so the call site must pass
one constant text per statement.

Run from the pipeline's environment, with the SQL parser::

    cd pipeline && uv run --with pglast==8.7 python ../tools/plugins/statements_build.py check --all
"""

from __future__ import annotations

import argparse
import ast
import importlib.util
import re
import sys
import tomllib
from collections.abc import Iterator
from dataclasses import dataclass
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
FIRST_PARTY = ROOT / "plugins" / "first-party"
sys.path.insert(0, str(ROOT / "plugins" / "sdk" / "python"))

from maktaba_plugin_sdk.statements import (  # noqa: E402
    Statement,
    StatementError,
    key,
    parse_file,
    render_file,
)

DB_METHODS = {"fetch", "fetchrow", "fetchval", "execute", "executemany"}
_SQL_START = re.compile(r"^\s*(SELECT|INSERT|UPDATE|DELETE|WITH|VALUES)\b", re.I)

#: tables only the queue engine writes (README §15.3): never a plugin's
ENGINE_TABLES = frozenset(
    {
        "processing_jobs",
        "job_stages",
        "job_events",
        "worker_pools",
        "workers",
        "cluster_node_stages",
        "events",
        "plugin_invocations",
        "plugin_grants",
        "plugin_installs",
        "app_settings",
        "home_secrets",
    }
)
#: tables a statement may read without listing them (rules: settings, scope-checked)
SETTINGS_TABLE = "app_settings"


@dataclass(frozen=True)
class Found:
    text: str | None
    file: str
    line: int
    func: str
    why: str = ""


def _const(node: ast.AST, names: dict[str, list[ast.AST]]) -> list[str] | None:
    """The constant texts an expression can take, or None when it is built at run time.
    An f-string counts when each of its parts is a constant (a module's ``SUCCESS``)."""
    if isinstance(node, ast.Constant) and isinstance(node.value, str | int):
        return [str(node.value)]
    if isinstance(node, ast.JoinedStr):
        acc = [""]
        for part in node.values:
            if isinstance(part, ast.FormattedValue):
                if part.format_spec is not None or part.conversion not in (-1, 115):
                    return None
                c = _const(part.value, names)
            else:
                c = _const(part, names)
            if c is None:
                return None
            acc = [a + b for a in acc for b in c]
        return acc
    if isinstance(node, ast.BinOp) and isinstance(node.op, ast.Add):
        a, b = _const(node.left, names), _const(node.right, names)
        if a is None or b is None:
            return None
        return [x + y for x in a for y in b]
    if isinstance(node, ast.IfExp):
        a, b = _const(node.body, names), _const(node.orelse, names)
        return None if a is None or b is None else a + b
    if isinstance(node, ast.Name) and node.id not in names and node.id in _IMPORTED:
        return [_IMPORTED[node.id]]
    if isinstance(node, ast.Name) and node.id in names:
        others = {k: x for k, x in names.items() if k != node.id}
        out: list[str] = []
        appends: list[list[str]] = []
        for v in names[node.id]:
            if isinstance(v, _Append):
                c = _const(v.value, others)
                if c is None:
                    return None
                appends.append(c)
                continue
            c = _const(v, others)
            if c is None:
                return None
            out += c
        # ``name += <text>`` in a branch: each append taken or not, in order (a statement
        # with an optional clause, as ``AND s.transcript_id = $2``)
        for suffixes in appends:
            out = out + [x + y for x in out for y in suffixes]
            if len(out) > 64:
                return None
        return out
    return None


class _Append(ast.AST):
    """``name += value``: a text the name may have had appended (see :func:`_const`)."""

    def __init__(self, value: ast.AST) -> None:
        super().__init__()
        self.value = value


#: names a scanned module imported as constant strings (``from ..x import SUCCESS``)
_IMPORTED: dict[str, str] = {}


def _imports(tree: ast.Module, module: str) -> dict[str, str]:
    """The string constants a module imports by name, read from the modules themselves."""
    import importlib

    out: dict[str, str] = {}
    pkg = module.rsplit(".", 1)[0] if "." in module else module
    for n in tree.body:
        if not isinstance(n, ast.ImportFrom):
            continue
        base = n.module or ""
        if n.level:
            parts = pkg.split(".")
            parts = parts[: len(parts) - (n.level - 1)] if n.level > 1 else parts
            base = ".".join(parts) + ("." + base if base else "")
        try:
            mod = importlib.import_module(base)
        except Exception:  # noqa: BLE001 - not importable here: its names stay unknown
            continue
        for a in n.names:
            v = getattr(mod, a.name, None)
            if isinstance(v, str | int) and not isinstance(v, bool):
                out[a.asname or a.name] = str(v)
    return out


def _assigns(body: list[ast.stmt]) -> dict[str, list[ast.AST]]:
    out: dict[str, list[ast.AST]] = {}
    for st in body:
        for n in ast.walk(st) if not isinstance(st, ast.FunctionDef | ast.AsyncFunctionDef) else ():
            if (
                isinstance(n, ast.Assign)
                and len(n.targets) == 1
                and isinstance(n.targets[0], ast.Name)
            ):
                out.setdefault(n.targets[0].id, []).append(n.value)
            elif isinstance(n, ast.AnnAssign) and isinstance(n.target, ast.Name) and n.value:
                out.setdefault(n.target.id, []).append(n.value)
    return out


def scan_source(
    src: str, file: str, *, module: str = "", only: set[str] | None = None
) -> list[Found]:
    """Every statement text passed to a database method in ``src``; ``only`` limits the scan
    to those top-level functions (a shared module whose other functions serve other stages)."""
    tree = ast.parse(src)
    _IMPORTED.clear()
    if module:
        _IMPORTED.update(_imports(tree, module))
    module_names = _assigns(tree.body)
    found: list[Found] = []

    def visit(node: ast.AST, func: str, names: dict[str, list[ast.AST]]) -> None:
        for child in ast.iter_child_nodes(node):
            if isinstance(child, ast.FunctionDef | ast.AsyncFunctionDef):
                if only is not None and func == "module" and child.name not in only:
                    continue
                local = dict(names)
                for k, v in _assigns(child.body).items():
                    local[k] = v
                for n in ast.walk(child):
                    if (
                        isinstance(n, ast.Assign)
                        and len(n.targets) == 1
                        and isinstance(n.targets[0], ast.Name)
                    ):
                        local.setdefault(n.targets[0].id, [])
                        if n.value not in local[n.targets[0].id]:
                            local[n.targets[0].id].append(n.value)
                    elif (
                        isinstance(n, ast.AugAssign)
                        and isinstance(n.op, ast.Add)
                        and isinstance(n.target, ast.Name)
                    ):
                        local.setdefault(n.target.id, []).append(_Append(n.value))
                visit(child, child.name, local)
                continue
            if (
                isinstance(child, ast.Call)
                and isinstance(child.func, ast.Attribute)
                and child.func.attr in DB_METHODS
                and child.args
            ):
                texts = _const(child.args[0], names)
                if texts is None:
                    arg = child.args[0]
                    if isinstance(arg, ast.JoinedStr | ast.BinOp):
                        found.append(Found(None, file, child.lineno, func, "built at run time"))
                    elif isinstance(arg, ast.Name | ast.Attribute | ast.Subscript | ast.Call):
                        found.append(Found(None, file, child.lineno, func, "not a constant text"))
                else:
                    for t in texts:
                        if _SQL_START.match(t):
                            found.append(Found(t, file, child.lineno, func))
            visit(child, func, names)

    visit(tree, "module", module_names)
    return found


def _module_file(dotted: str) -> Path:
    spec = importlib.util.find_spec(dotted)
    if spec is None or spec.origin is None:
        raise SystemExit(f"statements.toml: no module {dotted}")
    return Path(spec.origin)


def plugin_dirs() -> list[Path]:
    if not FIRST_PARTY.is_dir():
        return []
    return sorted(p for p in FIRST_PARTY.iterdir() if (p / "manifest.toml").is_file())


def sources(plugin: Path) -> Iterator[tuple[Path, str, str, set[str] | None]]:
    """``(file, label, module, only)`` for the plugin's own code and the shared modules its
    ``statements.toml`` lists (``"pkg.mod"`` or ``"pkg.mod:fn1,fn2"``)."""
    src = plugin / "src"
    for f in sorted(src.rglob("*.py")):
        rel = f.relative_to(src).with_suffix("")
        mod = ".".join(p for p in rel.parts if p != "__init__")
        yield f, (str(f.relative_to(ROOT)) if f.is_relative_to(ROOT) else str(f)), mod, None
    cfg = plugin / "statements.toml"
    if cfg.is_file():
        for entry in tomllib.loads(cfg.read_text())["modules"]:
            dotted, _, fns = entry.partition(":")
            only = {x.strip() for x in fns.split(",") if x.strip()} or None
            yield _module_file(dotted), dotted, dotted, only


def worker_functions(plugin: Path) -> set[str]:
    """``worker`` of statements.toml: the plugin's functions that run in the worker, never
    in the runner (a stage's ``refresh``: rules read and upkeep queued before each stage
    selection). They use the worker's own database connection, so their statements are
    neither registered nor checked."""
    cfg = plugin / "statements.toml"
    if not cfg.is_file():
        return set()
    return {str(x) for x in tomllib.loads(cfg.read_text()).get("worker", [])}


def extract(plugin: Path) -> tuple[list[Found], list[Found]]:
    ok: list[Found] = []
    bad: list[Found] = []
    worker = worker_functions(plugin)
    for f, label, mod, only in sources(plugin):
        for x in scan_source(f.read_text(encoding="utf-8"), label, module=mod, only=only):
            if only is None and x.func in worker:
                continue
            (bad if x.text is None else ok).append(x)
    return ok, bad


# --- the parse tree --------------------------------------------------------------------


def analyse(text: str) -> tuple[str, tuple[str, ...], tuple[str, ...], str, int, list[str]]:
    """``(mode, reads, writes, scope, scope_arg, problems)`` from the statement's parse tree."""
    from pglast import ast as pg
    from pglast import parse_sql
    from pglast.visitors import Visitor

    problems: list[str] = []
    try:
        stmts = parse_sql(text)
    except Exception as exc:  # noqa: BLE001 - a parse error is reported with the statement
        return "read", (), (), "catalog", 0, [f"does not parse: {exc}"]
    if len(stmts) != 1:
        problems.append("one statement per text")

    class V(Visitor):  # type: ignore[misc]
        def __init__(self) -> None:
            self.rels: list[str] = []
            self.targets: list[str] = []
            self.ctes: set[str] = set()
            self.locks = False
            self.lib_args: list[int] = []
            self.video_args: list[int] = []
            self.settings_args: list[int] = []
            self.state_write = False

        def visit_RangeVar(self, ancestors: Any, node: Any) -> None:  # noqa: N802
            self.rels.append(node.relname)

        def visit_CommonTableExpr(self, ancestors: Any, node: Any) -> None:  # noqa: N802
            self.ctes.add(node.ctename)

        def visit_LockingClause(self, ancestors: Any, node: Any) -> None:  # noqa: N802
            self.locks = True

        def visit_InsertStmt(self, ancestors: Any, node: Any) -> None:  # noqa: N802
            self.targets.append(node.relation.relname)
            # INSERT … (library_id, …) VALUES ($1, …): the row's library is the scope
            cols = [getattr(c, "name", "") for c in node.cols or ()]
            sel = node.selectStmt
            for vals in getattr(sel, "valuesLists", None) or ():
                for col, v in zip(cols, vals, strict=False):
                    if isinstance(v, pg.ParamRef):
                        if col == "library_id":
                            self.lib_args.append(v.number)
                        elif col == "video_id":
                            self.video_args.append(v.number)

        def visit_UpdateStmt(self, ancestors: Any, node: Any) -> None:  # noqa: N802
            self.targets.append(node.relation.relname)
            if node.relation.relname == "videos":
                for t in node.targetList or ():
                    if getattr(t, "name", None) == "state":
                        self.state_write = True

        def visit_DeleteStmt(self, ancestors: Any, node: Any) -> None:  # noqa: N802
            self.targets.append(node.relation.relname)

        def visit_A_Expr(self, ancestors: Any, node: Any) -> None:  # noqa: N802
            if not isinstance(node.name, tuple) or len(node.name) != 1:
                return
            if getattr(node.name[0], "sval", None) != "=":
                return
            for col, par in ((node.lexpr, node.rexpr), (node.rexpr, node.lexpr)):
                if isinstance(col, pg.ColumnRef) and isinstance(par, pg.ParamRef):
                    fields = [getattr(f, "sval", "") for f in col.fields]
                    if fields and fields[-1] == "key" and "app_settings" in self.rels:
                        self.settings_args.append(par.number)
                    elif fields and fields[-1] == "library_id":
                        self.lib_args.append(par.number)
                    elif fields and fields[-1] == "video_id":
                        self.video_args.append(par.number)

    v = V()
    v(stmts)
    tables = {r for r in v.rels if r not in v.ctes}
    writes = {t for t in v.targets if t not in v.ctes}
    if v.locks:
        writes |= tables  # SELECT … FOR UPDATE needs UPDATE on what it locks
    reads = tables - writes
    if v.state_write:
        problems.append("writes videos.state (only the engine writes it)")
    mode = "write" if writes else "read"
    scope, arg = "catalog", 0
    if "app_settings" in tables and len(set(v.settings_args)) == 1:
        scope, arg = "settings", v.settings_args[0]
    elif len(set(v.video_args)) == 1:
        # a video in the job's scope (its own, or one of its library's): the stricter test
        scope, arg = "subject", v.video_args[0]
    elif len(set(v.lib_args)) == 1:
        scope, arg = "library", v.lib_args[0]
    return mode, tuple(sorted(reads)), tuple(sorted(writes)), scope, arg, problems


# --- sync and check -------------------------------------------------------------------


def _existing(plugin: Path) -> dict[str, Statement]:
    out: dict[str, Statement] = {}
    q = plugin / "queries"
    for f in sorted(q.glob("*.sql")) if q.is_dir() else ():
        st = parse_file(f.read_text(encoding="utf-8"), path=str(f))
        out[key(st.text)] = st
    return out


def _stem(label: str) -> str:
    base = label.rsplit("/", 1)[-1].removesuffix(".py")
    return base.rsplit(".", 1)[-1].strip("_") or "module"


def wanted(plugin: Path) -> tuple[list[Statement], list[str]]:
    """The statements ``queries/`` should hold, and the problems found on the way."""
    found, bad = extract(plugin)
    problems = [f"{b.file}:{b.line} ({b.func}): a statement {b.why}" for b in bad]
    have = _existing(plugin)
    taken = {st.name for st in have.values()}
    out: dict[str, Statement] = {}
    for x in found:
        k = key(x.text or "")
        if k in out:
            continue
        mode, reads, writes, scope, arg, probs = analyse(k)
        problems += [f"{x.file}:{x.line}: {p}" for p in probs]
        old = have.get(k)
        if old is not None:
            name = old.name
        else:
            base = f"{_stem(x.file)}.{x.func.strip('_') or 'module'}"
            n = 1
            name = base
            while name in taken:
                n += 1
                name = f"{base}.{n}"
            taken.add(name)
        forced = _scopes(plugin).get(name)
        if forced:
            scope, _, n = forced.partition(" ")
            arg = int(n.strip().lstrip("$") or 0)
        out[k] = Statement(name, k, mode, scope, arg, reads, writes)
    return sorted(out.values(), key=lambda s: s.name), problems


def _scopes(plugin: Path) -> dict[str, str]:
    """``[scope]`` of statements.toml: a statement's scope set by hand (``catalog``, or
    ``library $2`` where the parse tree cannot tell), reviewed with the plugin."""
    cfg = plugin / "statements.toml"
    if not cfg.is_file():
        return {}
    return {str(k): str(v) for k, v in (tomllib.loads(cfg.read_text()).get("scope") or {}).items()}


def manifest_trusted(plugin: Path) -> dict[str, Any]:
    m = tomllib.loads((plugin / "manifest.toml").read_text(encoding="utf-8"))
    return dict(m.get("trusted") or {})


def check_tables(plugin: Path, sts: list[Statement]) -> list[str]:
    t = manifest_trusted(plugin)
    db = t.get("db") or {}
    may_read = set(db.get("read") or ()) | set(db.get("write") or ())
    may_write = set(db.get("write") or ())
    used_r: set[str] = set()
    used_w: set[str] = set()
    out: list[str] = []
    for st in sts:
        for tb in st.writes:
            used_w.add(tb)
            if tb in ENGINE_TABLES:
                out.append(f"{st.name}: writes {tb}, which only the queue engine writes")
            elif tb not in may_write:
                out.append(f"{st.name}: writes {tb}, not in [trusted] db.write")
        for tb in st.reads:
            used_r.add(tb)
            if tb == SETTINGS_TABLE:
                if st.scope != "settings":
                    out.append(f"{st.name}: reads {tb} without scope settings $N")
                continue
            if tb == "home_secrets":
                out.append(f"{st.name}: reads {tb}: secrets are never a plugin's")
            elif tb not in may_read:
                out.append(f"{st.name}: reads {tb}, not in [trusted] db.read or db.write")
    for tb in sorted(may_write - used_w):
        out.append(f"[trusted] db.write lists {tb}, which no statement writes")
    for tb in sorted(set(db.get("read") or ()) - used_r - used_w):
        out.append(f"[trusted] db.read lists {tb}, which no statement reads")
    return out


def sync(plugin: Path) -> list[str]:
    sts, problems = wanted(plugin)
    q = plugin / "queries"
    q.mkdir(exist_ok=True)
    keep = {f"{st.name}.sql" for st in sts}
    for f in q.glob("*.sql"):
        if f.name not in keep:
            f.unlink()
    for st in sts:
        (q / f"{st.name}.sql").write_text(render_file(st), encoding="utf-8")
    return problems + check_tables(plugin, sts)


def check(plugin: Path) -> list[str]:
    try:
        sts, problems = wanted(plugin)
        have = _existing(plugin)
    except StatementError as exc:
        return [str(exc)]
    names = {st.name: st for st in have.values()}
    for st in sts:
        old = names.get(st.name)
        if old is None or key(old.text) != key(st.text):
            problems.append(f"queries/{st.name}.sql is missing or stale (run sync)")
        elif (old.mode, old.reads, old.writes) != (st.mode, st.reads, st.writes):
            problems.append(f"queries/{st.name}.sql: header says {old.mode} {old.tables}")
    wanted_names = {st.name for st in sts}
    for n in sorted(set(names) - wanted_names):
        problems.append(f"queries/{n}.sql: no code runs it (run sync)")
    return problems + check_tables(plugin, list(have.values()))


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("action", choices=["sync", "check"])
    ap.add_argument("plugin", nargs="?", help="a folder name under plugins/first-party")
    ap.add_argument("--all", action="store_true")
    ap.add_argument("--dir", help="a plugin folder outside plugins/first-party (the tests' toy)")
    args = ap.parse_args(argv)
    if args.dir:
        dirs = [Path(args.dir).resolve()]
    else:
        dirs = plugin_dirs() if args.all or not args.plugin else [FIRST_PARTY / args.plugin]
    failed = False
    for d in dirs:
        problems = sync(d) if args.action == "sync" else check(d)
        for p in problems:
            print(f"{d.name}: {p}", file=sys.stderr)  # noqa: T201
        failed |= bool(problems)
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
