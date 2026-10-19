"""The HTML reports follow the app's theme rule: the browser's
prefers-color-scheme, dark when it states no preference, CSS only (they are
served under a CSP that allows no script)."""

from __future__ import annotations

from maktaba_pipeline.engine import plan_page
from maktaba_pipeline.engine.stages import review_pages
from maktaba_pipeline.schedule.channel_audit import html_report


def _dark_by_default(page: str) -> None:
    assert "color-scheme:dark light" in page
    assert "prefers-color-scheme:light" in page
    assert "prefers-color-scheme:dark" not in page  # light is the exception, not dark
    assert "<script" not in page.lower()


def test_plan_page_is_dark_unless_the_os_asks_for_light() -> None:
    _dark_by_default(plan_page._CSS)


def test_review_page_is_dark_unless_the_os_asks_for_light() -> None:
    _dark_by_default(review_pages.render("Lib", [], {}, {}))


def test_audit_page_is_dark_unless_the_os_asks_for_light() -> None:
    _dark_by_default(html_report([], 14))
