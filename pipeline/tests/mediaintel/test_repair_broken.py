from pathlib import Path

from scripts.mediaintel import db, jobs, repair_broken


def test_download_args_write_locally_under_a_plain_name(tmp_path: Path):
    target = Path("Y:/x/مفاتح الطلب 2025⧸1446 ｜ 100% [1].mp4")
    args = repair_broken.download_args("a-B_9", target, None, tmp_path)
    assert args[-1] == "https://www.youtube.com/watch?v=a-B_9"
    assert args[args.index("-f") + 1] == repair_broken.FORMAT_MP4
    assert Path(args[args.index("-o") + 1]) == tmp_path / "v-a-B_9.%(ext)s"
    mkv = repair_broken.download_args("a", Path("y.mkv"), "firefox", tmp_path)
    assert mkv[mkv.index("-f") + 1] == repair_broken.FORMAT and "--cookies-from-browser" in mkv


def test_only_finished_downloads_count(tmp_path: Path):
    (tmp_path / "v-a.f137.mp4.part").write_bytes(b"x")
    (tmp_path / "v-a.webm").write_bytes(b"x")
    assert [p.name for p in repair_broken._made(tmp_path)] == ["v-a.webm"]


def test_broken_lists_failed_probes_with_their_youtube_id(tmp_path: Path):
    conn = db.connect(str(tmp_path / "m.db"))
    conn.executemany(
        "INSERT INTO files (path, folder, stem, ext, size, mtime, youtube_id) "
        "VALUES (?,'Y:/x',?,'.mp4',1,1.0,?)",
        [("a.mp4", "a", "ya"), ("b.mp4", "b", None)],
    )
    jobs.ensure(conn)
    jobs.record(conn, "a.mp4", "probe", 1, 1, 1.0, "broken file")
    jobs.record(conn, "b.mp4", "probe", 1, 1, 1.0, "broken file")
    jobs.record(conn, "c.mp4", "probe", 1, 1, 1.0, None)
    assert repair_broken.broken(conn) == [("a.mp4", "ya"), ("b.mp4", None)]


def test_a_second_backup_never_overwrites_the_first(tmp_path: Path):
    target = tmp_path / "a.mp4"
    (tmp_path / "a.mp4.broken").write_bytes(b"old")
    assert repair_broken._backup_name(target).name == "a.mp4.broken2"


def test_a_file_that_reads_now_is_kept_not_downloaded(tmp_path: Path, monkeypatch):
    conn = db.connect(str(tmp_path / "m.db"))
    conn.execute("INSERT INTO files (path, folder, stem, ext, size, mtime, youtube_id) "
                 "VALUES ('a.mp4','Y:/x','a','.mp4',1,1.0,'ya')")  # fmt: skip
    jobs.ensure(conn)
    jobs.record(conn, "a.mp4", "probe", 1, 1, 1.0, "ffprobe gave no duration")
    monkeypatch.setattr(repair_broken, "check", lambda p: (60.0, 1920, 1080, ""))
    monkeypatch.setattr(repair_broken, "repair_one", lambda *a, **k: (_ for _ in ()).throw(
        AssertionError("must not download")))  # fmt: skip
    assert repair_broken.run(conn)["readable"] == 1
    assert repair_broken.broken(conn) == []
    # a share that does not answer is no reason to replace a file
    jobs.record(conn, "a.mp4", "probe", 1, 1, 1.0, "ffprobe gave no duration")
    busy = (None, None, None, "could not read it (TimeoutExpired)")
    monkeypatch.setattr(repair_broken, "check", lambda p: busy)
    assert repair_broken.run(conn)["failed"] == 1
