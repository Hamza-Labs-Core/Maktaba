from pathlib import Path

from scripts.mediaintel import db, jobs, repair_broken


def test_download_args_keep_the_files_container_beside_it():
    args = repair_broken.download_args("abc", Path("Y:/x/درس.mp4"), None)
    assert args[-1] == "https://www.youtube.com/watch?v=abc"
    assert args[args.index("--merge-output-format") + 1] == "mp4"
    assert args[args.index("-o") + 1].endswith("درس.repair.%(ext)s")
    assert "--cookies-from-browser" in repair_broken.download_args("a", Path("y.mkv"), "firefox")


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
