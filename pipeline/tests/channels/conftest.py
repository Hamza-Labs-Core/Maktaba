# the real migrated database (SQLite, and Postgres with MAKTABA_TEST_PG_URL) for the tests
# that ask for `db`
from tests.engine.fixtures_db import db, pg_template, sqlite_template  # noqa: F401
