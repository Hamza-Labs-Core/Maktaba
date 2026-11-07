"""The parity harness's database URLs: a test URL's query (``sslmode=disable``) is kept when
the harness names another database, and the plugin API's DSN turns TLS off only when the
URL does not say (lib/pq requires it by default; the test servers have none)."""

from __future__ import annotations

import pytest

from tests.plugins.firstparty.harness import _go_dsn, _swap_db


@pytest.mark.parametrize(
    ("url", "want"),
    [
        ("postgres://u@h:5432/postgres", "postgres://u@h:5432/maktaba_pa_1"),
        (
            "postgres://u@127.0.0.1:56411/postgres?sslmode=disable",
            "postgres://u@127.0.0.1:56411/maktaba_pa_1?sslmode=disable",
        ),
        (
            "postgres://u:p@h/db?sslmode=require&x=1",
            "postgres://u:p@h/maktaba_pa_1?sslmode=require&x=1",
        ),
    ],
)
def test_another_database_keeps_the_query(url: str, want: str) -> None:
    assert _swap_db(url, "maktaba_pa_1") == want


def test_the_plugin_api_turns_tls_off_only_when_the_url_does_not_say() -> None:
    assert _go_dsn("postgres://u@h/db") == "postgres://u@h/db?sslmode=disable"
    assert _go_dsn("postgres://u@h/db?sslmode=disable") == "postgres://u@h/db?sslmode=disable"
    assert _go_dsn("postgres://u@h/db?sslmode=require") == "postgres://u@h/db?sslmode=require"
