"""devportal_follows.py: which changes ask for a maktaba.dev update."""

from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import devportal_follows as df  # noqa: E402


def test_a_new_page_without_the_portal_warns():
    out = df.findings([("A", "web/src/pages/review/Review.tsx"), ("M", "web/src/i18n/en.json")])
    assert out == ["new screen: web/src/pages/review/Review.tsx"]


def test_the_portal_changed_too_says_nothing():
    changes = [("A", "web/src/pages/review/Review.tsx"), ("M", "web/packages/devportal/scripts/apps.mjs")]
    assert df.findings(changes) == []
    assert df.findings([("A", "web/src/pages/X.tsx"), ("A", "docs/developers/plugins/x.md")]) == []


def test_a_changed_page_is_not_a_new_one():
    assert df.findings([("M", "web/src/pages/Home.tsx")]) == []


def test_tests_and_previews_are_not_screens():
    for p in (
        "web/src/pages/review/Review.test.tsx",
        "web/src/pages/Login.cases.test.tsx",
        "apps/android/player/src/test/java/x/HomeScreenTest.kt",
        "apps/apple/PlayerUITests/HomeView.swift",
        "apps/apple/Player/HomeViewPreviews.swift",
    ):
        assert not df.new_screen(p), p


def test_native_screens():
    assert df.new_screen("apps/android/remote/src/main/java/x/review/ReviewScreen.kt")
    assert df.new_screen("apps/tv/android/app/src/main/java/x/LibraryPage.kt")
    assert df.new_screen("apps/desktop-player/src/main/kotlin/x/live/LiveTvScreen.kt")
    assert df.new_screen("apps/apple/Remote/Review/ReviewView.swift")
    assert df.new_screen("apps/tv/tvos/MaktabaTV/LibraryView.swift")
    assert not df.new_screen("apps/android/core/src/main/java/x/PlayQueueModel.kt")


def test_the_plugin_contract_changes_with_any_edit():
    out = df.findings([("M", "shared/plugins/manifest.schema.json"), ("M", "api/internal/plugins/capabilities.go")])
    assert out == [
        "plugin system: shared/plugins/manifest.schema.json",
        "plugin system: api/internal/plugins/capabilities.go",
    ]
    assert df.findings([("M", "api/internal/plugins/capabilities_test.go")]) == []


def test_new_api_paths():
    before = {"paths": {"/api/a": {}}}
    after = {"paths": {"/api/a": {}, "/api/b": {}}}
    assert df.new_api_paths(before, after) == ["/api/b"]
    assert df.new_api_paths(None, None) == []
    assert df.findings([("M", "shared/api/openapi.json")], ["/api/b"]) == ["new API path: /api/b"]


def test_the_trailer():
    m = df.TRAILER.findall("fix: x\n\nNo-Portal-Change: an internal split, no new screen\n")
    assert m == ["an internal split, no new screen"]
