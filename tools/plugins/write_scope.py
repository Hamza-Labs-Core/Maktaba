"""The scope checks a first-party plugin's write statement needs (Epic 54, README §15.12).

A write addresses rows by ids the plugin passes: ``UPDATE series_seasons … WHERE id = $1``,
``INSERT INTO promo_links (video_id, library_id, series_id, season_id, …)``. The plugin
API checks an argument against the job's token only when the statement's header names it
(``scope: season $1``), so the build works the checks out here, from the parse tree and
``scope_tables.toml`` (which column of which table holds which kind of id), and fails a
write it cannot bind:

* ``UPDATE`` / ``DELETE``: the ``WHERE`` must have a top-level ``AND`` term that ties the
  rows to an owner column (``col = $N``, ``col = ANY($N)``, ``col IN (SELECT value FROM
  json_each($N))``, or ``col IN (SELECT r.c FROM r WHERE …)`` whose inner rows are bound
  the same way); each such ``$N`` is checked. An owner column the ``SET`` gives a ``$N``
  (``video_id = CASE WHEN $27 THEN $28 …``) is checked too.
* ``INSERT``: every owner column given a ``$N`` is checked, and at least one must be;
  ``ON CONFLICT … DO UPDATE`` must have an owner column in its conflict target (or a
  ``DO UPDATE … WHERE t.col = EXCLUDED.col``), so the update cannot reach another scope's
  row.
* A table listed under ``[home]`` needs no check; one in neither list fails.

Reads by id are worked out here too (:func:`read_checks`): every ``$N`` a column of an
owner table is compared to, anywhere in a statement, is checked.

What the parse tree cannot prove is set by hand in the plugin's ``statements.toml``
(``[scope."name"]`` with ``checks`` and a ``reason``), reviewed with the plugin.
"""

from __future__ import annotations

import tomllib
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

TABLES_FILE = Path(__file__).with_name("scope_tables.toml")

Check = tuple[str, int]


@dataclass(frozen=True)
class Tables:
    #: table -> column -> the kind of id it holds
    owners: dict[str, dict[str, str]]
    #: table -> {"reason": …, "catalog_only": bool}
    home: dict[str, dict[str, Any]]
    #: "table.column" -> why an id-named column of a listed table is no owner (a read may
    #: compare it to a ``$N`` unchecked)
    not_ids: dict[str, str] = field(default_factory=dict)


def load(path: Path = TABLES_FILE, kinds: tuple[str, ...] | None = None) -> Tables:
    """``scope_tables.toml``; a kind the API does not resolve, a table in both lists, a
    home table with no reason, or a ``[not_ids]`` entry with no reason (or naming an owner
    column, or a table not listed) is an error."""
    d = tomllib.loads(path.read_text(encoding="utf-8"))
    owners = {str(t): {str(c): str(k) for c, k in cols.items()} for t, cols in d["tables"].items()}
    home = {str(t): dict(v) for t, v in d["home"].items()}
    not_ids = {str(k): str(v).strip() for k, v in (d.get("not_ids") or {}).items()}
    for t in sorted(set(owners) & set(home)):
        raise ValueError(f"{path.name}: {t} is in [tables] and [home]")
    for t, v in home.items():
        if not str(v.get("reason") or "").strip():
            raise ValueError(f"{path.name}: [home.{t}] needs a reason")
    for k, why in not_ids.items():
        t, _, c = k.partition(".")
        if not why:
            raise ValueError(f"{path.name}: [not_ids] {k} needs a reason")
        if t not in owners:
            raise ValueError(f"{path.name}: [not_ids] {k}: {t} is not in [tables]")
        if c in owners[t]:
            raise ValueError(f"{path.name}: [not_ids] {k} is an owner column")
    if kinds is not None:
        for t, cols in owners.items():
            for c, k in cols.items():
                if k not in kinds:
                    raise ValueError(
                        f"{path.name}: {t}.{c}: kind {k!r} is not one the API resolves"
                    )
    return Tables(owners, home, not_ids)


# --- the parse tree ----------------------------------------------------------------------


def _pg() -> Any:
    from pglast import ast

    return ast


class _Enums:
    def __getattr__(self, name: str) -> Any:
        from pglast import enums as e

        return getattr(e, name)


enums = _Enums()


def _param(e: Any) -> int | None:
    """``$N`` or ``$N::type``."""
    pg = _pg()
    while isinstance(e, pg.TypeCast):
        e = e.arg
    return e.number if isinstance(e, pg.ParamRef) else None


def _has_param(e: Any) -> bool:
    from pglast.visitors import Visitor

    pg = _pg()
    if isinstance(e, pg.ParamRef):
        return True
    found = False

    class V(Visitor):  # type: ignore[misc]
        def visit_ParamRef(self, ancestors: Any, node: Any) -> None:  # noqa: N802
            nonlocal found
            found = True

    if e is not None:
        V()(e)
    return found


def _value_params(e: Any) -> list[int] | None:
    """The ``$N`` an owner column may take its value from, or None for an expression the
    build cannot follow. A ``CASE``'s conditions are not values; a column or constant is
    no argument."""
    pg = _pg()
    n = _param(e)
    if n is not None:
        return [n]
    if e is None or isinstance(e, pg.A_Const | pg.ColumnRef):
        return []
    if isinstance(e, pg.TypeCast):
        return _value_params(e.arg)
    if isinstance(e, pg.CaseExpr):
        out: list[int] = []
        for w in e.args or ():
            got = _value_params(w.result)
            if got is None:
                return None
            out += got
        got = _value_params(e.defresult)
        return None if got is None else out + got
    if isinstance(e, pg.CoalesceExpr):
        out = []
        for a in e.args or ():
            got = _value_params(a)
            if got is None:
                return None
            out += got
        return out
    return None if _has_param(e) else []


def _col(e: Any, names: set[str], *, qualified_only: bool = False) -> str | None:
    """The column of the target table a reference names (``col``, ``t.col``, ``alias.col``)."""
    pg = _pg()
    if not isinstance(e, pg.ColumnRef):
        return None
    fields = [getattr(f, "sval", None) for f in e.fields]
    if any(f is None for f in fields):
        return None
    if len(fields) == 1 and not qualified_only:
        return str(fields[0])
    if len(fields) == 2 and fields[0] in names:
        return str(fields[1])
    return None


def _conjuncts(where: Any) -> list[Any]:
    pg = _pg()
    if where is None:
        return []
    if isinstance(where, pg.BoolExpr) and where.boolop == enums.BoolExprType.AND_EXPR:
        out: list[Any] = []
        for a in where.args:
            out += _conjuncts(a)
        return out
    return [where]


def _names(rel: Any) -> set[str]:
    out = {rel.relname}
    if getattr(rel, "alias", None) is not None:
        out = {rel.alias.aliasname}
    return out


def _bound(
    where: Any, owners: dict[str, str], names: set[str], tables: Tables, *, qualified_only: bool
) -> list[Check]:
    """The checks a ``WHERE``'s top-level ``AND`` terms bind the target's rows with."""
    pg = _pg()
    out: list[Check] = []
    for c in _conjuncts(where):
        if isinstance(c, pg.A_Expr) and _op(c) == "=":
            if c.kind == enums.A_Expr_Kind.AEXPR_OP:
                pairs = ((c.lexpr, c.rexpr), (c.rexpr, c.lexpr))
            elif c.kind == enums.A_Expr_Kind.AEXPR_OP_ANY:
                pairs = ((c.lexpr, c.rexpr),)
            else:
                continue
            for a, b in pairs:
                col, n = _col(a, names, qualified_only=qualified_only), _param(b)
                if col in owners and n is not None:
                    out.append((owners[col], n))
        elif isinstance(c, pg.SubLink) and c.subLinkType == enums.SubLinkType.ANY_SUBLINK:
            col = _col(c.testexpr, names, qualified_only=qualified_only)
            if col in owners:
                out += _sub_bound(c.subselect, owners[col], tables)
    return out


def _sub_bound(sel: Any, kind: str, tables: Tables) -> list[Check]:
    """``IN (SELECT value FROM json_each($N))``, or ``IN (SELECT r.c FROM r WHERE …)`` with
    ``r.c`` an id of the same kind and ``r``'s rows bound."""
    pg = _pg()
    if not isinstance(sel, pg.SelectStmt) or len(sel.targetList or ()) != 1:
        return []
    if len(sel.fromClause or ()) != 1:
        return []
    src = sel.fromClause[0]
    if isinstance(src, pg.RangeFunction):
        ((fn, _),) = src.functions
        name = [getattr(x, "sval", "") for x in fn.funcname]
        if name == ["json_each"] and len(fn.args or ()) == 1:
            n = _param(fn.args[0])
            return [] if n is None else [(kind, n)]
        return []
    if not isinstance(src, pg.RangeVar):
        return []
    inner = tables.owners.get(src.relname) or {}
    names = _names(src)
    col = _col(sel.targetList[0].val, names)
    if inner.get(col or "") != kind:
        return []
    return _bound(sel.whereClause, inner, names, tables, qualified_only=False)


def _op(e: Any) -> str:
    name = e.name or ()
    return str(getattr(name[-1], "sval", "")) if name else ""


def _excluded_pairs(where: Any, names: set[str]) -> set[str]:
    """Columns a ``DO UPDATE … WHERE t.col = EXCLUDED.col`` keeps to the inserted row's."""
    pg = _pg()
    out: set[str] = set()
    for c in _conjuncts(where):
        if not (isinstance(c, pg.A_Expr) and _op(c) == "="):
            continue
        for a, b in ((c.lexpr, c.rexpr), (c.rexpr, c.lexpr)):
            col = _col(a, names, qualified_only=True)
            ex = _col(b, {"excluded", "EXCLUDED"}, qualified_only=True)
            if col is not None and col == ex:
                out.add(col)
    return out


def _update(node: Any, tables: Tables) -> tuple[list[Check], list[str]]:
    rel = node.relation.relname
    owners = tables.owners[rel]
    names = _names(node.relation) | {rel}
    joined = bool(getattr(node, "fromClause", None) or getattr(node, "usingClause", None))
    checks = _bound(node.whereClause, owners, names, tables, qualified_only=joined)
    problems: list[str] = []
    if not checks:
        verb = "updates" if isinstance(node, _pg().UpdateStmt) else "deletes from"
        problems.append(
            f"{verb} {rel} without tying its rows to a checked id"
            f" (a WHERE term on one of {sorted(owners)} = $N)"
        )
    for t in getattr(node, "targetList", None) or ():
        if t.name in owners:
            got = _value_params(t.val)
            if got is None:
                problems.append(f"sets {rel}.{t.name} from an expression the build cannot check")
            else:
                checks += [(owners[t.name], n) for n in got]
    return checks, problems


def _insert(node: Any, tables: Tables) -> tuple[list[Check], list[str]]:
    pg = _pg()
    rel = node.relation.relname
    owners = tables.owners[rel]
    cols = [getattr(c, "name", "") for c in node.cols or ()]
    sel = node.selectStmt
    checks: list[Check] = []
    problems: list[str] = []
    bound_cols: set[str] = set()
    if sel is not None and sel.valuesLists:
        for row in sel.valuesLists:
            for col, v in zip(cols, row, strict=False):
                if col not in owners:
                    continue
                got = _value_params(v)
                if got is None:
                    problems.append(
                        f"inserts {rel}.{col} from an expression the build cannot check"
                    )
                    continue
                checks += [(owners[col], n) for n in got]
                if got:
                    bound_cols.add(col)
    elif sel is not None and sel.targetList:
        src = sel.fromClause[0] if len(sel.fromClause or ()) == 1 else None
        src_bound: list[Check] = []
        src_owners: dict[str, str] = {}
        src_names: set[str] = set()
        if isinstance(src, pg.RangeVar) and src.relname in tables.owners:
            src_owners = tables.owners[src.relname]
            src_names = _names(src) | {src.relname}
            src_bound = _bound(sel.whereClause, src_owners, src_names, tables, qualified_only=False)
        for col, t in zip(cols, sel.targetList, strict=False):
            if col not in owners:
                continue
            got = _value_params(t.val)
            ref = _col(t.val, src_names) if src_names else None
            if got:
                checks += [(owners[col], n) for n in got]
                bound_cols.add(col)
            elif ref is not None and src_owners.get(ref) == owners[col] and src_bound:
                checks += src_bound
                bound_cols.add(col)
            else:
                problems.append(
                    f"inserts {rel}.{col} from rows the build cannot tie to a checked id"
                )
    else:
        problems.append(f"inserts into {rel} in a form the build does not read")
    if not bound_cols and not problems:
        problems.append(f"inserts into {rel} with none of {sorted(owners)} from a checked $N")
    oc = node.onConflictClause
    if oc is not None and oc.action == enums.OnConflictAction.ONCONFLICT_UPDATE:
        target = (
            {getattr(e, "name", None) for e in (oc.infer.indexElems or ())} if oc.infer else set()
        )
        kept = _excluded_pairs(oc.whereClause, {rel} | _names(node.relation))
        if not ((target | kept) & bound_cols):
            problems.append(
                f"ON CONFLICT … DO UPDATE on {rel} may update another scope's row"
                f" (its conflict target has none of {sorted(bound_cols or owners)})"
            )
        for t in oc.targetList or ():
            if t.name in owners:
                got = _value_params(t.val)
                if got is None:
                    problems.append(
                        f"sets {rel}.{t.name} from an expression the build cannot check"
                    )
                else:
                    checks += [(owners[t.name], n) for n in got]
    return checks, problems


def write_checks(text: str, tables: Tables) -> tuple[list[Check], list[str]]:
    """``(checks, problems)`` for one statement: the checks every write in it needs, sorted
    by argument, and what the build could not bind. Tables not listed are problems too."""
    from pglast import parse_sql
    from pglast.visitors import Visitor

    pg = _pg()
    nodes: list[Any] = []

    class V(Visitor):  # type: ignore[misc]
        def visit_InsertStmt(self, ancestors: Any, node: Any) -> None:  # noqa: N802
            nodes.append(node)

        def visit_UpdateStmt(self, ancestors: Any, node: Any) -> None:  # noqa: N802
            nodes.append(node)

        def visit_DeleteStmt(self, ancestors: Any, node: Any) -> None:  # noqa: N802
            nodes.append(node)

    V()(parse_sql(text))
    ctes: set[str] = set()

    class C(Visitor):  # type: ignore[misc]
        def visit_CommonTableExpr(self, ancestors: Any, node: Any) -> None:  # noqa: N802
            ctes.add(node.ctename)

    C()(parse_sql(text))
    checks: list[Check] = []
    problems: list[str] = []
    for node in nodes:
        rel = node.relation.relname
        if rel in ctes or rel in tables.home:
            continue
        if rel not in tables.owners:
            problems.append(f"writes {rel}, which tools/plugins/scope_tables.toml does not list")
            continue
        got, probs = (
            _insert(node, tables) if isinstance(node, pg.InsertStmt) else _update(node, tables)
        )
        checks += got
        problems += probs
    return sorted(set(checks), key=lambda c: (c[1], c[0])), problems


# --- reads by id (README §15.12) ---------------------------------------------------------
#
# A statement reads a row by id wherever it compares a column of a table to a ``$N``: the
# same ``$N`` is then the plugin's choice of whose row it reads. Every such comparison on an
# owner column is checked, wherever it stands (a WHERE, a JOIN's ON, a subquery, the source
# rows of a write): ``col = $N``, ``col IS NOT DISTINCT FROM $N``, ``col = ANY($N)``,
# ``col IN ($N, $M)``, and ``col IN (SELECT … FROM fn($N))`` (json_each, unnest). A table
# compared to a ``$N`` must be listed (``[tables]`` or ``[home]``), and an id-named column
# of a listed table (``id``, ``…_id``) must be an owner column or a ``[not_ids]`` entry
# with its reason.


def _scope(node: Any, ctes: set[str]) -> dict[str, str | None]:
    """The names a statement's FROM (and an UPDATE's or DELETE's target) brings in: name or
    alias -> table, None for a CTE, a subquery or a function."""
    pg = _pg()
    out: dict[str, str | None] = {}

    def add(items: Any) -> None:
        for f in items or ():
            if isinstance(f, pg.RangeVar):
                name = f.alias.aliasname if f.alias is not None else f.relname
                out[name] = None if (f.relname in ctes and not f.schemaname) else f.relname
            elif isinstance(f, pg.JoinExpr):
                add([f.larg, f.rarg])
            elif isinstance(f, pg.RangeSubselect | pg.RangeFunction) and f.alias is not None:
                out[f.alias.aliasname] = None

    if isinstance(node, pg.SelectStmt):
        add(node.fromClause)
    elif isinstance(node, pg.UpdateStmt):
        add([node.relation])
        add(node.fromClause)
    elif isinstance(node, pg.DeleteStmt):
        add([node.relation])
        add(node.usingClause)
    return out


_UNKNOWN = "?"
#: the owner columns of the analysis running (set by :func:`read_checks`)
_OWNERS: dict[str, dict[str, str]] = {}


def _column(e: Any, scopes: list[dict[str, str | None]]) -> tuple[str | None, str] | None:
    """``(table, column)`` a column reference reads: None for no column, table None for a
    CTE's or a subquery's column, ``"?"`` when the build cannot tell which table."""
    pg = _pg()
    if not isinstance(e, pg.ColumnRef):
        return None
    fields = [getattr(f, "sval", None) for f in e.fields]
    if any(f is None for f in fields) or len(fields) not in (1, 2):
        return None
    col = str(fields[-1])
    if len(fields) == 2:
        for s in reversed(scopes):
            if fields[0] in s:
                return s[str(fields[0])], col
        return _UNKNOWN, col
    inner = scopes[-1] if scopes else {}
    rels = list(inner.values())
    if len(rels) == 1:
        return rels[0], col
    # a column only one of the joined tables has (Postgres refuses an ambiguous one): the
    # owner table that lists it, when only one does
    own = [r for r in rels if r is not None and col in (_OWNERS.get(r) or {})]
    if len(own) == 1:
        return own[0], col
    return _UNKNOWN, col


def _id_named(col: str) -> bool:
    return col == "id" or col.endswith("_id")


def read_checks(text: str, tables: Tables) -> tuple[list[Check], list[str]]:
    """``(checks, problems)``: a check for every ``$N`` a column of an owner table is
    compared to, anywhere in the statement, and what the build could not type."""
    from pglast import parse_sql

    pg = _pg()
    tree = parse_sql(text)
    _OWNERS.clear()
    _OWNERS.update(tables.owners)
    ctes: set[str] = set()

    def collect(node: Any) -> None:
        if isinstance(node, list | tuple):
            for x in node:
                collect(x)
            return
        if not isinstance(node, pg.Node):
            return
        if isinstance(node, pg.CommonTableExpr):
            ctes.add(node.ctename)
        for f in node:
            collect(getattr(node, f))

    collect(tree)
    checks: set[Check] = set()
    problems: list[str] = []

    def compared(col: Any, n: int | None, scopes: list[dict[str, str | None]]) -> None:
        if n is None:
            return
        ref = _column(col, scopes)
        if ref is None:
            return
        table, c = ref
        if table is None:
            return  # a CTE's or a subquery's column: its own rows are checked where read
        if table == _UNKNOWN:
            if _id_named(c):
                problems.append(f"compares {c} to ${n}: qualify it with its table")
            return
        if table in tables.home:
            return
        if table not in tables.owners:
            problems.append(
                f"reads {table} by {c} = ${n}, which tools/plugins/scope_tables.toml does not list"
            )
            return
        kind = tables.owners[table].get(c)
        if kind is not None:
            checks.add((kind, n))
        elif _id_named(c) and f"{table}.{c}" not in tables.not_ids:
            problems.append(
                f"reads {table} by {c} = ${n}: scope_tables.toml has no kind for {table}.{c}"
                " (an owner column, or a [not_ids] entry with its reason)"
            )

    def walk(node: Any, scopes: list[dict[str, str | None]]) -> None:
        if isinstance(node, list | tuple):
            for x in node:
                walk(x, scopes)
            return
        if not isinstance(node, pg.Node):
            return
        if isinstance(node, pg.SelectStmt | pg.UpdateStmt | pg.DeleteStmt):
            scopes = [*scopes, _scope(node, ctes)]
        if isinstance(node, pg.A_Expr) and _op(node) == "=":
            k = node.kind
            if k in (enums.A_Expr_Kind.AEXPR_OP, enums.A_Expr_Kind.AEXPR_NOT_DISTINCT):
                compared(node.lexpr, _param(node.rexpr), scopes)
                compared(node.rexpr, _param(node.lexpr), scopes)
            elif k == enums.A_Expr_Kind.AEXPR_OP_ANY:
                compared(node.lexpr, _param(node.rexpr), scopes)
            elif k == enums.A_Expr_Kind.AEXPR_IN:
                for r in node.rexpr or ():
                    compared(node.lexpr, _param(r), scopes)
        elif isinstance(node, pg.SubLink) and node.subLinkType == enums.SubLinkType.ANY_SUBLINK:
            sel = node.subselect
            if (
                isinstance(sel, pg.SelectStmt)
                and len(sel.fromClause or ()) == 1
                and isinstance(sel.fromClause[0], pg.RangeFunction)
            ):
                for fn, _ in sel.fromClause[0].functions:
                    for a in getattr(fn, "args", None) or ():
                        compared(node.testexpr, _param(a), scopes)
        for f in node:
            walk(getattr(node, f), scopes)

    walk(tree, [])
    return sorted(checks, key=lambda c: (c[1], c[0])), problems
