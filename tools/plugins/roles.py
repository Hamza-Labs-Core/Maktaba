"""The first-party plugins' Postgres roles (Epic 54, story 54.24 §3).

For each plugin in ``plugins/first-party/``, ``maktaba_fp_<name>`` is a ``NOLOGIN`` role
with ``SELECT`` on its ``[trusted] db.read`` tables and ``SELECT, INSERT, UPDATE, DELETE``
on its ``db.write`` tables (and their sequences), nothing else: every grant it had is
revoked first, so a table a manifest no longer lists loses its grant. The plugin API's
statements run under it (``SET LOCAL ROLE``), so Postgres refuses anything else as a
second layer. The home's own role (the database owner) and ``maktaba_worker`` (every
joined machine's login role) are made members so that ``SET ROLE`` works on every machine's
API (story 54.24 §5); a member gains no rights without ``SET ROLE`` (``NOINHERIT`` on
Postgres 16, ``INHERIT FALSE``).

``sql`` prints the migration body for the current manifests; ``check <migration>`` (CI)
fails when the newest roles migration is not what ``sql`` prints, so a manifest change
ships with its migration. SQLite has no roles: its sibling is a no-op.

A core stage that must write under a role of its own, and has no plugin manifest because
it runs in the worker (it needs the home's language model door, which a plugin's runner
does not have), is listed in :data:`STAGE_ROLES`. ``stage-sql`` prints its role, which
goes in the migration that makes the stage's table; ``check`` fails when the newest
migration that makes the role is not what ``stage-sql`` prints.
"""

from __future__ import annotations

import argparse
import re
import sys
import tomllib
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
FIRST_PARTY = ROOT / "plugins" / "first-party"
MIGRATIONS = ROOT / "shared" / "db" / "migrations"

_IDENT = re.compile(r"^[a-z][a-z0-9_]*$")

#: Core stages with a role of their own: the tables each reads and the tables it writes.
#: ``content_screen`` (Epic 57, story 57.17) writes what it detected and nothing else:
#: ``videos`` is read only, so the stage cannot set ``videos.age_tier`` (the API's
#: recompute is its one writer).
STAGE_ROLES: dict[str, dict[str, tuple[str, ...]]] = {
    "content_screen": {
        "read": (
            "app_settings",
            "libraries",
            "transcript_segments",
            "transcripts",
            "video_topic_tags",
            "videos",
        ),
        "write": ("video_content_signals",),
    },
}


def role_of(plugin_id: str) -> str:
    short = plugin_id.removeprefix("tv.maktaba.")
    return "maktaba_fp_" + re.sub(r"[-.]", "_", short)


def manifests(root: Path = FIRST_PARTY) -> list[dict[str, object]]:
    if not root.is_dir():
        return []
    return [
        tomllib.loads((p / "manifest.toml").read_text(encoding="utf-8"))
        for p in sorted(root.iterdir())
        if (p / "manifest.toml").is_file()
    ]


def role_sql(m: dict[str, object]) -> str:
    pid = str(m["id"])
    role = role_of(pid)
    trusted = m.get("trusted") or {}
    assert isinstance(trusted, dict)
    db = trusted.get("db") or {}
    assert isinstance(db, dict)
    reads = sorted(set(db.get("read") or ()) - set(db.get("write") or ()))
    writes = sorted(set(db.get("write") or ()))
    return _role_block(pid, role, reads, writes)


def stage_role_of(stage: str) -> str:
    return "maktaba_fp_" + stage


def stage_role_sql(stage: str) -> str:
    """The role of a core stage in :data:`STAGE_ROLES`, in the plugins' own form."""
    tables = STAGE_ROLES[stage]
    writes = sorted(set(tables.get("write") or ()))
    reads = sorted(set(tables.get("read") or ()) - set(writes))
    return _role_block(f"core stage {stage}", stage_role_of(stage), reads, writes)


def stage_body() -> str:
    return "\n".join(stage_role_sql(s) for s in sorted(STAGE_ROLES))


def _role_block(pid: str, role: str, reads: list[str], writes: list[str]) -> str:
    for t in [*reads, *writes]:
        if not _IDENT.match(t):
            raise SystemExit(f"{pid}: table name {t!r}")
    lines = [
        f"-- {pid}: {role}",
        "-- +goose StatementBegin",
        "DO $$",
        "DECLARE",
        "    t text;",
        "    sq text;",
        "BEGIN",
        f"    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '{role}') THEN",
        "        -- a home whose role may not create roles (Maktaba.app before its cluster",
        "        -- bootstrap) has none: its stages run in-process (native.choose)",
        "        IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = current_user",
        "                       AND (rolsuper OR rolcreaterole)) THEN",
        f"            RAISE NOTICE '{role}: not made (no right to create roles)';",
        "            RETURN;",
        "        END IF;",
        f"        CREATE ROLE {role} NOLOGIN NOINHERIT;",
        "    END IF;",
        f"    EXECUTE 'REVOKE ALL ON ALL TABLES IN SCHEMA public FROM {role}';",
        f"    EXECUTE 'REVOKE ALL ON ALL SEQUENCES IN SCHEMA public FROM {role}';",
        f"    GRANT USAGE ON SCHEMA public TO {role};",
    ]
    if reads:
        lines.append(f"    FOREACH t IN ARRAY ARRAY[{', '.join(repr(x) for x in reads)}] LOOP")
        lines.append(
            "        -- a table made later (segment_embeddings, with pgvector) is granted then"
        )
        lines.append("        CONTINUE WHEN to_regclass(format('public.%I', t)) IS NULL;")
        lines.append(f"        EXECUTE format('GRANT SELECT ON %I TO {role}', t);")
        lines.append("    END LOOP;")
    if writes:
        lines.append(f"    FOREACH t IN ARRAY ARRAY[{', '.join(repr(x) for x in writes)}] LOOP")
        lines.append("        CONTINUE WHEN to_regclass(format('public.%I', t)) IS NULL;")
        lines.append(
            f"        EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO {role}', t);"
        )
        lines.append("        -- the table's own sequences (serial and identity columns)")
        lines.append(
            "        FOR sq IN SELECT s.relname FROM pg_class s JOIN pg_depend d ON d.objid = s.oid"
        )
        lines.append("                  JOIN pg_class c ON c.oid = d.refobjid")
        lines.append("                 WHERE s.relkind = 'S' AND c.relname = t LOOP")
        lines.append(
            f"            EXECUTE format('GRANT USAGE, SELECT, UPDATE ON SEQUENCE %I TO {role}', sq);"  # noqa: E501
        )
        lines.append("        END LOOP;")
        lines.append("    END LOOP;")
    lines += [
        "    -- every machine's API may SET ROLE to it; membership alone gives no right",
        "    -- (a role made by another database's owner on a shared Postgres may not be ours to",
        "    -- grant: then this home's stages run in-process, native.choose)",
        "    BEGIN",
        f"        EXECUTE format('GRANT {role} TO %I', current_user);",
        "        IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN",
        "            BEGIN",
        f"                EXECUTE 'GRANT {role} TO maktaba_worker WITH INHERIT FALSE';",
        "            EXCEPTION WHEN syntax_error THEN",
        f"                EXECUTE 'GRANT {role} TO maktaba_worker';",
        "            END;",
        "        END IF;",
        "    EXCEPTION WHEN insufficient_privilege THEN",
        f"        RAISE NOTICE '{role}: not ours to grant';",
        "    END;",
        "END;",
        "$$;",
        "-- +goose StatementEnd",
    ]
    return "\n".join(lines) + "\n"


def body(root: Path = FIRST_PARTY) -> str:
    return "\n".join(role_sql(m) for m in manifests(root))


def newest_roles_migration() -> Path | None:
    found = sorted(MIGRATIONS.glob("[0-9][0-9][0-9][0-9]_fp_roles*.sql"))
    found = [p for p in found if not p.name.endswith(".sqlite.sql")]
    return found[-1] if found else None


def newest_stage_role_migration(stage: str) -> Path | None:
    """The newest Postgres migration that makes the stage's role."""
    needle = f"CREATE ROLE {stage_role_of(stage)} "
    found = [
        p
        for p in sorted(MIGRATIONS.glob("[0-9][0-9][0-9][0-9]_*.sql"))
        if not p.name.endswith(".sqlite.sql") and needle in p.read_text(encoding="utf-8")
    ]
    return found[-1] if found else None


def check_stages() -> list[str]:
    problems = []
    for stage in sorted(STAGE_ROLES):
        mig = newest_stage_role_migration(stage)
        if mig is None:
            problems.append(
                f"no migration makes {stage_role_of(stage)}: write one from"
                " `python3 tools/plugins/roles.py stage-sql`"
            )
        elif stage_role_sql(stage) not in mig.read_text(encoding="utf-8"):
            problems.append(
                f"{mig.name} is not the role of stage {stage} in STAGE_ROLES: a new migration"
            )
    return problems


def check() -> list[str]:
    problems = check_stages()
    want = body()
    mig = newest_roles_migration()
    if not want.strip():
        return problems
    if mig is None:
        return [
            *problems,
            "no NNNN_fp_roles migration: write one from `python3 tools/plugins/roles.py sql`",
        ]
    text = mig.read_text(encoding="utf-8")
    if want not in text:
        problems.append(
            f"{mig.name} is not the roles of today's manifests: a new NNNN_fp_roles migration"
        )
    return problems


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("action", choices=["sql", "stage-sql", "check"])
    ap.add_argument("--dir", help="a folder of plugin folders (tests)")
    args = ap.parse_args(argv)
    if args.action == "sql":
        sys.stdout.write(body(Path(args.dir)) if args.dir else body())
        return 0
    if args.action == "stage-sql":
        sys.stdout.write(stage_body())
        return 0
    problems = check()
    for p in problems:
        print(f"roles: {p}", file=sys.stderr)  # noqa: T201
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
