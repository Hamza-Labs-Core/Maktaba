"""The ledger's two updates are fixed texts (Epic 54 wave 5: a first-party plugin runs
named statements): every writable field once, in FIELD_ORDER, set when given, else kept."""

from __future__ import annotations

import re

from maktaba_pipeline.library_mgmt import ledger


def test_the_fixed_updates_name_every_writable_field_in_order() -> None:
    assert set(ledger.FIELD_ORDER) == set(ledger._FIELDS)
    for sql, first in ((ledger._ADVANCE_SQL, 5), (ledger._UPDATE_SQL, 3)):
        got = re.findall(r"(\w+) = CASE WHEN \$(\d+) THEN \$(\d+) ELSE (\w+) END", sql)
        assert [g[0] for g in got] == list(ledger.FIELD_ORDER)
        for i, (name, flag, value, kept) in enumerate(got):
            assert (int(flag), int(value), kept) == (first + 2 * i, first + 2 * i + 1, name)
