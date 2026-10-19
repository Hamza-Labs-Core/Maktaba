from maktaba_pipeline.engine.stages.fingerprint import incomplete
from scripts.mediaintel.scan_local import probe_failure
from scripts.mediaintel.signatures import _incomplete

ONE_HASH = "0f0f0f0f0f0f0f0f"


def test_a_flat_frame_is_not_a_failed_read() -> None:
    # five frame times, four hashes: one frame was black (read, but no hash)
    frames = ",".join([ONE_HASH] * 4)
    assert _incomplete(1000.0, "h", "t", frames, unread=0) is None
    assert incomplete(1000.0, "h", "t", frames, 0) is None


def test_an_unread_frame_still_is() -> None:
    assert _incomplete(1000.0, "h", "t", ONE_HASH, unread=2) == "read failed: 2 frames"
    assert incomplete(1000.0, "h", "t", ONE_HASH, 2) == "read failed: 2 frames"
    assert _incomplete(1000.0, None, "t", ONE_HASH, unread=0) == "read failed: head"


def test_a_stored_signature_is_judged_by_its_audio_only() -> None:
    assert _incomplete(1000.0, "h", "t", "") is None


def test_probe_failure_names_a_broken_file() -> None:
    err = "[mov,mp4,m4a,3gp,3g2,mj2 @ 0000028d] moov atom not found\nX.mp4: Invalid data"
    assert probe_failure(err) == "broken file: unfinished download (moov atom not found)"
    assert probe_failure("").startswith("ffprobe gave no duration")
