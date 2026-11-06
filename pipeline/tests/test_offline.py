"""#640: MAKTABA_OFFLINE, read as the Go side (ports.Offline) reads it."""

from __future__ import annotations

import pytest

from maktaba_pipeline import offline

pytestmark = pytest.mark.unit


@pytest.mark.parametrize(
    ("value", "want"),
    [
        ("", False),
        ("0", False),
        ("off", False),
        ("1", True),
        ("true", True),
        ("ON", True),
        (" yes ", True),
    ],  # fmt: skip
)
def test_the_switch_reads_like_the_servers(
    monkeypatch: pytest.MonkeyPatch, value: str, want: bool
) -> None:
    monkeypatch.setenv(offline.ENV, value)
    assert offline.offline() is want


def test_check_refuses_while_offline(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv(offline.ENV, "1")
    with pytest.raises(offline.Offline, match="ar.wikipedia.org"):
        offline.check("ar.wikipedia.org")
    monkeypatch.setenv(offline.ENV, "")
    offline.check("ar.wikipedia.org")  # online: no error
