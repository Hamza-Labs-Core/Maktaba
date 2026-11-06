"""Golden table for error-cause normalisation (plan 06-11 section 6.3)."""

from __future__ import annotations

import pytest

from maktaba_pipeline.engine.causes import brief, classify, norm

pytestmark = pytest.mark.unit

GOLDEN = [
    # (kind, message, cause)
    (
        "ffmpeg",
        "Error opening input: Invalid argument (ffmpeg read failed)",
        "nas_invalid_argument",
    ),
    ("ffmpeg", "[Errno 110] Connection timed out reading Y:/a/b.mp4", "nas_timeout"),
    ("os", "[WinError 121] The semaphore timeout period has expired", "nas_timeout"),
    ("os", "STATUS_IO_TIMEOUT on \\\\nas\\share\\x.mkv", "nas_timeout"),
    ("fp_partial", "read failed: head, 2 frames", "nas_partial_read"),
    ("os", "FileNotFoundError: [Errno 2] No such file or directory: '/a/b.mp4'", "file_missing"),
    ("os", "[WinError 3] The system cannot find the path", "file_missing"),
    ("ffmpeg", "Invalid data found when processing input", "ffmpeg_decode"),
    ("ffmpeg", "moov atom not found", "ffmpeg_decode"),
    ("yt", "ERROR: Sign in to confirm you're not a bot", "youtube_bot_check"),
    ("yt", "Video unavailable. This video is private", "youtube_unavailable"),
    ("plex", "Plex answered HTTP 401 Unauthorized", "plex_auth"),
    ("plex", "Plex: connection refused at http://192.168.1.5:32400", "plex_unreachable"),
    ("dizquetv", "dizqueTV: connect call failed ('127.0.0.1', 8002)", "dizquetv_unreachable"),
    ("plex", "read-back mismatch for collection 12", "readback_mismatch"),
    ("dispatch_unknown_stage", "no handler registered for stage foo", "no_handler"),
    ("graph_violation", "detect requested probe, not in may_request", "graph_violation"),
    ("db", "asyncpg.exceptions.DeadlockDetectedError: deadlock detected", "db_error"),
    ("db", "sqlite3.OperationalError: database is locked", "db_error"),
    # Epic 40
    (
        "DiskFullError",
        'could not resize shared memory segment "/PostgreSQL.1" to 1048576 bytes',
        "shm_full",
    ),
    ("OSError", "[Errno 28] No space left on device", "disk_full"),
    ("PermissionError", "[Errno 13] Permission denied: '/v/a.mp4'", "permission_denied"),
    ("os", "[WinError 5] Access is denied", "permission_denied"),
    ("file_missing", "/lib/a.mp4: not found", "file_missing"),
    ("ffmpeg", "thumbnail failed: poster rc=1 tail=''", "ffmpeg_rc"),
    ("chapter_frames_failed", "exit 1", "ffmpeg_rc"),
    ("ffmpeg", "thumbnail failed: poster rc=1 tail='moov atom not found'", "ffmpeg_decode"),
]


@pytest.mark.parametrize(("kind", "message", "cause"), GOLDEN)
def test_known_patterns(kind: str, message: str, cause: str) -> None:
    assert classify(kind, message) == cause


def test_a_kind_that_is_a_cause_code_is_the_cause() -> None:
    # transcribe raises nas_timeout with "<file>: no answer"; an Arabic name with spaces
    # is not all <path>, and each file made a group of its own, titled by its key
    msg = "/v/شاهين/المعجزات والخوارق..برهان النبوة الثاني 2 | دلائل النبوة 3.mp4: no answer"
    assert classify("nas_timeout", msg) == "nas_timeout"
    assert classify("file_missing", "gone") == "file_missing"
    # a kind that is not a cause code still groups by its normalised message
    assert classify("model_unavailable", "4 of 4 clips got no answer from the model") == (
        "model_unavailable:<n> of <n> clips got no answer from the model"
    )


def test_brief_keeps_the_start_and_cuts_at_a_word() -> None:
    msg = "thumbnail failed: ffmpeg rc=234 tail='" + "Error opening output thread " * 20 + "'"
    b = brief(msg)
    assert b.startswith("thumbnail failed: ffmpeg rc=234")
    assert len(b) <= 300
    assert b.endswith("…")
    assert b[:-1].endswith(("Error", "opening", "output", "thread"))
    assert brief("  short  ") == "short"


def test_plex_timeout_is_not_a_nas_timeout() -> None:
    assert classify("plex", "Plex request timed out") == "plex_unreachable"


def test_unknown_failures_group_by_kind_and_normalised_message() -> None:
    a = classify("boom", "failed on 'C:\\x\\y 12.mp4' after 3 tries")
    b = classify("boom", "failed on 'D:\\other\\z 99.mp4' after 7 tries")
    assert a == b == "boom:failed on <path> after <n> tries"


@pytest.mark.parametrize(
    "message",
    [
        "Y:/a/b/c.mp4 not readable (id 123e4567-e89b-12d3-a456-426614174000)",
        "deadbeefcafe failed at /var/lib/x after 4.5 s",
        "see \\\\nas\\share\\f.mkv line 99",
        "plain words only",
    ],
)
def test_norm_is_idempotent(message: str) -> None:
    once = norm(message)
    assert norm(once) == once


def test_norm_replaces_ids_hex_numbers_and_paths() -> None:
    out = norm("job 5 on /mnt/x/y.mkv uuid 123e4567-e89b-12d3-a456-426614174000 hash deadbeef01")
    assert out == "job <n> on <path> uuid <id> hash <hex>"


def test_norm_truncates() -> None:
    assert len(norm("x " * 200)) <= 120


@pytest.mark.parametrize(
    "cause",
    [
        "plugin_trap",
        "plugin_timeout",
        "plugin_memory",
        "plugin_capability",
        "plugin_scope",
        "plugin_quota",
        "plugin_failed",
        "plugin_held",
    ],
)
def test_plugin_causes_are_their_own_codes(cause: str) -> None:
    """Epic 54 (54.06 §3): a plugin job's end keeps its cause, whatever the message."""
    assert classify(cause, "wasm backtrace at 0x1f in /x/plugin.wasm") == cause
