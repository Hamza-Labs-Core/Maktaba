-- Deterministic Postgres seed for the queue graph (plan 06-11 section 11.1, "Test fixture for B").
--
-- Apply AFTER migrations 0001-0091. It inserts two libraries, six videos, two seasons and a
-- queue with jobs in every state, several failure causes, review/waiting outcomes, log lines,
-- live events, health samples, two worker hosts and one extension. Every id and timestamp is
-- fixed (base time 2026-01-01 12:00:00+00), so Go integration tests can assert golden JSON.
--
-- Ids: libraries ...0a01/0a02, videos ...0b01-0b06, series ...0c01, seasons ...0d01/0d02.
-- job_stages and worker_pools are already seeded by slot 0087 (write_mode 'auto').

INSERT INTO libraries (id, name, roots) VALUES
 ('00000000-0000-0000-0000-000000000a01', 'Lectures', ARRAY['Y:/lectures']),
 ('00000000-0000-0000-0000-000000000a02', 'Dramas',   ARRAY['Y:/dramas'])
ON CONFLICT (id) DO NOTHING;

INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime, title, duration_sec, created_at, updated_at) VALUES
 ('00000000-0000-0000-0000-000000000b01', '00000000-0000-0000-0000-000000000a01', md5('b01') || md5('b01x'), 'Y:/lectures/ep01.mp4', 'ep01.mp4', 1001, '2025-12-01 00:00:00+00', 'Episode 1', 1500, '2025-12-01 00:00:00+00', '2025-12-01 00:00:00+00'),
 ('00000000-0000-0000-0000-000000000b02', '00000000-0000-0000-0000-000000000a01', md5('b02') || md5('b02x'), 'Y:/lectures/ep02.mp4', 'ep02.mp4', 1002, '2025-12-01 00:00:00+00', 'Episode 2', 1510, '2025-12-01 00:00:00+00', '2025-12-01 00:00:00+00'),
 ('00000000-0000-0000-0000-000000000b03', '00000000-0000-0000-0000-000000000a01', md5('b03') || md5('b03x'), 'Y:/lectures/ep03.mp4', 'ep03.mp4', 1003, '2025-12-01 00:00:00+00', 'Episode 3', 1520, '2025-12-01 00:00:00+00', '2025-12-01 00:00:00+00'),
 ('00000000-0000-0000-0000-000000000b04', '00000000-0000-0000-0000-000000000a01', md5('b04') || md5('b04x'), 'Y:/lectures/talk.mp4', 'talk.mp4', 1004, '2025-12-01 00:00:00+00', 'A loose talk', 2400, '2025-12-01 00:00:00+00', '2025-12-01 00:00:00+00'),
 ('00000000-0000-0000-0000-000000000b05', '00000000-0000-0000-0000-000000000a02', md5('b05') || md5('b05x'), 'Y:/dramas/d01.mkv', 'd01.mkv', 1005, '2025-12-01 00:00:00+00', 'Drama 1', 2700, '2025-12-01 00:00:00+00', '2025-12-01 00:00:00+00'),
 ('00000000-0000-0000-0000-000000000b06', '00000000-0000-0000-0000-000000000a02', md5('b06') || md5('b06x'), 'Y:/dramas/d02.mkv', 'd02.mkv', 1006, '2025-12-01 00:00:00+00', 'Drama 2', 2710, '2025-12-01 00:00:00+00', '2025-12-01 00:00:00+00')
ON CONFLICT (id) DO NOTHING;

INSERT INTO series (id, library_id, name, metadata) VALUES
 ('00000000-0000-0000-0000-000000000c01', '00000000-0000-0000-0000-000000000a01', 'Stories', '{"series_key":"stories","kind":"episodic"}')
ON CONFLICT (id) DO NOTHING;

INSERT INTO series_seasons (id, series_id, library_id, season, kind, name, first_air, last_air, median_duration_s, episode_count, membership_hash, detector_version) VALUES
 ('00000000-0000-0000-0000-000000000d01', '00000000-0000-0000-0000-000000000c01', '00000000-0000-0000-0000-000000000a01', 1, 'episodic', 'Stories', '2025-03-01', '2025-03-30', 1510, 3, 'seedhash1', 2),
 ('00000000-0000-0000-0000-000000000d02', '00000000-0000-0000-0000-000000000c01', '00000000-0000-0000-0000-000000000a01', 2, 'ongoing',  'Stories', '2026-01-01', NULL,         1500, 0, 'seedhash2', 2)
ON CONFLICT (id) DO NOTHING;

INSERT INTO series_episodes (series_id, video_id, season, episode) VALUES
 ('00000000-0000-0000-0000-000000000c01', '00000000-0000-0000-0000-000000000b01', 1, 1),
 ('00000000-0000-0000-0000-000000000c01', '00000000-0000-0000-0000-000000000b02', 1, 2),
 ('00000000-0000-0000-0000-000000000c01', '00000000-0000-0000-0000-000000000b03', 1, 3)
ON CONFLICT DO NOTHING;

-- ------------------------------------------------------------------ jobs (explicit ids 1..)
-- columns: id, subject, stage, state, priority, attempts, created_at, plus per-row extras

INSERT INTO processing_jobs
 (id, subject_kind, subject_id, video_id, library_id, season_id, stage, state, priority, attempts, max_attempts,
  not_before, claimed_by, claimed_at, last_heartbeat_at, started_at, finished_at, created_at,
  stage_version, input_hash, output_hash, outcome, error, error_cause, progress, reasons, enqueue_count, triggered_by, triggered_by_stage, propagated_at)
VALUES
 -- ready (pending, no wait)
 (1,  'video','00000000-0000-0000-0000-000000000b01','00000000-0000-0000-0000-000000000b01','00000000-0000-0000-0000-000000000a01',NULL,'probe','pending',100,0,3, NULL,NULL,NULL,NULL,NULL,NULL,'2026-01-01 11:50:00+00', NULL,NULL,NULL,NULL,NULL,NULL,NULL,'[{"why":"scan","by":"system","at":"2026-01-01T11:50:00Z"}]',1,NULL,NULL,NULL),
 (2,  'video','00000000-0000-0000-0000-000000000b02','00000000-0000-0000-0000-000000000b02','00000000-0000-0000-0000-000000000a01',NULL,'thumb_hash','pending',100,0,3, NULL,NULL,NULL,NULL,NULL,NULL,'2026-01-01 11:51:00+00', NULL,NULL,NULL,NULL,NULL,NULL,NULL,'[]',1,1,'probe',NULL),
 -- debouncing (not_before in the future, never attempted)
 (3,  'library','00000000-0000-0000-0000-000000000a01',NULL,'00000000-0000-0000-0000-000000000a01',NULL,'detect','pending',100,0,3, '2026-01-01 12:05:00+00',NULL,NULL,NULL,NULL,NULL,'2026-01-01 11:55:00+00', NULL,NULL,NULL,NULL,NULL,NULL,NULL,'[{"why":"probe output changed","by":"job:1","from_stage":"probe"}]',4,1,'probe',NULL),
 -- backoff (not_before in the future after a failed attempt)
 (4,  'video','00000000-0000-0000-0000-000000000b03','00000000-0000-0000-0000-000000000b03','00000000-0000-0000-0000-000000000a01',NULL,'fingerprint','pending',100,1,3, '2026-01-01 12:10:00+00',NULL,NULL,NULL,NULL,NULL,'2026-01-01 11:40:00+00', NULL,NULL,NULL,NULL,'{"kind":"OSError","message":"[WinError 121] The semaphore timeout period has expired","retryable":true}','nas_timeout',NULL,'[]',1,NULL,NULL,NULL),
 -- claimed / running (with progress) / resuming-free
 (5,  'video','00000000-0000-0000-0000-000000000b05','00000000-0000-0000-0000-000000000b05','00000000-0000-0000-0000-000000000a02',NULL,'probe','claimed',100,1,3, NULL,'PC/100/aaaa','2026-01-01 11:59:50+00','2026-01-01 11:59:55+00',NULL,NULL,'2026-01-01 11:30:00+00', NULL,NULL,NULL,NULL,NULL,NULL,NULL,'[]',1,NULL,NULL,NULL),
 (6,  'video','00000000-0000-0000-0000-000000000b06','00000000-0000-0000-0000-000000000b06','00000000-0000-0000-0000-000000000a02',NULL,'fingerprint','running',100,1,3, NULL,'PC/100/aaaa','2026-01-01 11:58:00+00','2026-01-01 11:59:58+00','2026-01-01 11:58:05+00',NULL,'2026-01-01 11:30:00+00', NULL,NULL,NULL,NULL,NULL,NULL,'{"step":"audio","steps":["audio","frames"],"done_units":1,"total_units":2,"message":"intro window"}','[]',1,NULL,NULL,NULL),
 (7,  'season','00000000-0000-0000-0000-000000000d01',NULL,'00000000-0000-0000-0000-000000000a01','00000000-0000-0000-0000-000000000d01','rate_style','running',100,1,3, NULL,'PC/100/aaaa','2026-01-01 11:57:00+00','2026-01-01 11:59:58+00','2026-01-01 11:57:02+00',NULL,'2026-01-01 11:30:00+00', NULL,NULL,NULL,NULL,NULL,NULL,'{"step":"sampling","steps":["sampling","rating"],"done_units":2,"total_units":3}','[]',1,2,'detect',NULL),
 -- paused
 (8,  'video','00000000-0000-0000-0000-000000000b04','00000000-0000-0000-0000-000000000b04','00000000-0000-0000-0000-000000000a01',NULL,'transcribe','paused',100,1,3, NULL,NULL,NULL,'2026-01-01 11:00:00+00','2026-01-01 11:00:30+00',NULL,'2026-01-01 10:55:00+00', NULL,NULL,NULL,NULL,NULL,NULL,NULL,'[]',1,NULL,NULL,NULL),
 -- done: changed / unchanged / skipped_unchanged / imported / awaiting_review / waiting_inputs
 (9,  'video','00000000-0000-0000-0000-000000000b01','00000000-0000-0000-0000-000000000b01','00000000-0000-0000-0000-000000000a01',NULL,'thumb_hash','done',100,1,3, NULL,NULL,NULL,NULL,'2026-01-01 11:00:00+00','2026-01-01 11:00:02+00','2026-01-01 10:59:00+00', 1,'in-a','out-a','changed',NULL,NULL,NULL,'[]',1,NULL,NULL,'2026-01-01 11:00:02+00'),
 (10, 'video','00000000-0000-0000-0000-000000000b01','00000000-0000-0000-0000-000000000b01','00000000-0000-0000-0000-000000000a01',NULL,'thumb_hash','done',100,1,3, NULL,NULL,NULL,NULL,'2026-01-01 11:30:00+00','2026-01-01 11:30:00+00','2026-01-01 11:29:59+00', 1,'in-a','out-a','skipped_unchanged',NULL,NULL,NULL,'[]',1,NULL,NULL,'2026-01-01 11:30:00+00'),
 (11, 'library','00000000-0000-0000-0000-000000000a01',NULL,'00000000-0000-0000-0000-000000000a01',NULL,'detect','done',100,1,3, NULL,NULL,NULL,NULL,'2026-01-01 10:00:30+00','2026-01-01 10:00:00+00','2026-01-01 09:59:00+00', 2,'in-d','out-d','changed',NULL,NULL,NULL,'[]',3,NULL,NULL,'2026-01-01 10:00:30+00'),
 (12, 'library','00000000-0000-0000-0000-000000000a01',NULL,'00000000-0000-0000-0000-000000000a01',NULL,'detect','done',100,1,3, NULL,NULL,NULL,NULL,'2026-01-01 11:00:30+00','2026-01-01 11:00:00+00','2026-01-01 10:59:00+00', 2,'in-d2','out-d','unchanged',NULL,NULL,NULL,'[]',1,NULL,NULL,'2026-01-01 11:00:30+00'),
 (13, 'season','00000000-0000-0000-0000-000000000d01',NULL,'00000000-0000-0000-0000-000000000a01','00000000-0000-0000-0000-000000000d01','tag','done',100,1,3, NULL,NULL,NULL,NULL,'2026-01-01 10:10:00+00','2026-01-01 10:09:58+00','2026-01-01 10:05:00+00', 1,'in-t','out-t','imported',NULL,NULL,NULL,'[]',1,NULL,NULL,'2026-01-01 10:10:00+00'),
 (14, 'catalog','00000000-0000-0000-0000-00000000ca7a',NULL,NULL,NULL,'write_channels','done',100,1,3, NULL,NULL,NULL,NULL,'2026-01-01 11:20:00+00','2026-01-01 11:19:50+00','2026-01-01 11:19:00+00', 1,'in-w','out-w','awaiting_review','{"channels":3}',NULL,NULL,'[]',1,NULL,NULL,'2026-01-01 11:20:00+00'),
 (15, 'season','00000000-0000-0000-0000-000000000d02',NULL,'00000000-0000-0000-0000-000000000a01','00000000-0000-0000-0000-000000000d02','rate_style','done',100,1,3, NULL,NULL,NULL,NULL,'2026-01-01 11:40:00+00','2026-01-01 11:39:58+00','2026-01-01 11:39:00+00', 1,NULL,'wait','waiting_inputs',NULL,NULL,NULL,'[]',1,NULL,NULL,'2026-01-01 11:40:00+00'),
 -- failed: three nas_timeout (one group), a bot check, a graph violation
 (16, 'video','00000000-0000-0000-0000-000000000b01','00000000-0000-0000-0000-000000000b01','00000000-0000-0000-0000-000000000a01',NULL,'fingerprint','failed',100,3,3, NULL,NULL,NULL,NULL,'2026-01-01 11:10:00+00','2026-01-01 11:00:00+00','2026-01-01 10:50:00+00', NULL,NULL,NULL,NULL,'{"kind":"OSError","message":"[WinError 121] timed out","retryable":true}','nas_timeout',NULL,'[]',1,NULL,NULL,NULL),
 (17, 'video','00000000-0000-0000-0000-000000000b02','00000000-0000-0000-0000-000000000b02','00000000-0000-0000-0000-000000000a01',NULL,'fingerprint','failed',100,3,3, NULL,NULL,NULL,NULL,'2026-01-01 11:11:00+00','2026-01-01 11:01:00+00','2026-01-01 10:50:00+00', NULL,NULL,NULL,NULL,'{"kind":"OSError","message":"semaphore timeout","retryable":true}','nas_timeout',NULL,'[]',1,NULL,NULL,NULL),
 (18, 'video','00000000-0000-0000-0000-000000000b05','00000000-0000-0000-0000-000000000b05','00000000-0000-0000-0000-000000000a02',NULL,'style_sample','failed',100,3,3, NULL,NULL,NULL,NULL,'2026-01-01 11:12:00+00','2026-01-01 11:02:00+00','2026-01-01 10:50:00+00', NULL,NULL,NULL,NULL,'{"kind":"OSError","message":"ETIMEDOUT","retryable":true}','nas_timeout',NULL,'[]',1,NULL,NULL,NULL),
 (19, 'library','00000000-0000-0000-0000-000000000a02',NULL,'00000000-0000-0000-0000-000000000a02',NULL,'youtube_sync','failed',100,1,3, NULL,NULL,NULL,NULL,'2026-01-01 11:15:00+00','2026-01-01 11:14:00+00','2026-01-01 11:00:00+00', NULL,NULL,NULL,NULL,'{"kind":"BotCheck","message":"Sign in to confirm you are not a bot","retryable":false}','youtube_bot_check',NULL,'[]',1,NULL,NULL,NULL),
 (20, 'season','00000000-0000-0000-0000-000000000d01',NULL,'00000000-0000-0000-0000-000000000a01','00000000-0000-0000-0000-000000000d01','tag','failed',100,1,3, NULL,NULL,NULL,NULL,'2026-01-01 11:16:00+00','2026-01-01 11:15:00+00','2026-01-01 11:00:00+00', NULL,NULL,NULL,NULL,'{"kind":"graph_violation","message":"tag requested probe","retryable":false}','graph_violation',NULL,'[]',1,NULL,NULL,NULL),
 -- a retried failure that is no longer the latest row of its subject (not an open issue)
 (21, 'video','00000000-0000-0000-0000-000000000b03','00000000-0000-0000-0000-000000000b03','00000000-0000-0000-0000-000000000a01',NULL,'thumb_hash','failed',100,3,3, NULL,NULL,NULL,NULL,'2026-01-01 10:30:00+00','2026-01-01 10:20:00+00','2026-01-01 10:10:00+00', NULL,NULL,NULL,NULL,'{"kind":"x","message":"old","retryable":true}','x:old',NULL,'[]',1,NULL,NULL,NULL),
 (22, 'video','00000000-0000-0000-0000-000000000b03','00000000-0000-0000-0000-000000000b03','00000000-0000-0000-0000-000000000a01',NULL,'thumb_hash','done',100,1,3, NULL,NULL,NULL,NULL,'2026-01-01 10:45:00+00','2026-01-01 10:44:59+00','2026-01-01 10:40:00+00', 1,'in-z','out-z','changed',NULL,NULL,NULL,'[]',1,21,'thumb_hash','2026-01-01 10:45:00+00'),
 -- cancelled
 (23, 'video','00000000-0000-0000-0000-000000000b06','00000000-0000-0000-0000-000000000b06','00000000-0000-0000-0000-000000000a02',NULL,'probe','cancelled',100,0,3, NULL,NULL,NULL,NULL,NULL,'2026-01-01 09:00:30+00','2026-01-01 09:00:00+00', NULL,NULL,NULL,NULL,NULL,NULL,NULL,'[]',1,NULL,NULL,NULL)
ON CONFLICT (id) DO NOTHING;

SELECT setval(pg_get_serial_sequence('processing_jobs', 'id'), 1000, false);

-- ------------------------------------------------------------------ log, events, health, workers

INSERT INTO job_events (job_id, subject_kind, subject_id, library_id, stage, at, level, kind, message, data) VALUES
 (9,  'video', '00000000-0000-0000-0000-000000000b01', '00000000-0000-0000-0000-000000000a01', 'thumb_hash', '2026-01-01 11:00:02+00', 'info',  'done',   'Thumbnail: changed', NULL),
 (10, 'video', '00000000-0000-0000-0000-000000000b01', '00000000-0000-0000-0000-000000000a01', 'thumb_hash', '2026-01-01 11:30:00+00', 'debug', 'skipped','inputs unchanged; skipped', NULL),
 (11, 'library','00000000-0000-0000-0000-000000000a01','00000000-0000-0000-0000-000000000a01', 'detect',     '2026-01-01 10:00:30+00', 'info',  'applied','detect: 1 added', '{"changes":{"kind":"detect","counts":{"added":1,"removed":0,"changed":0,"reordered":0},"added":[{"season_id":"00000000-0000-0000-0000-000000000d01","name":"Stories","kind":"episodic","number":1,"episodes":3}],"removed":[],"changed":[],"reordered":[]}}'),
 (11, 'library','00000000-0000-0000-0000-000000000a01','00000000-0000-0000-0000-000000000a01', 'detect',     '2026-01-01 10:00:31+00', 'info',  'triggered','queued tag, rate_style, plan_channels', '{"to":[{"stage":"tag","subject_kind":"season","outcome":"inserted"}]}'),
 (16, 'video', '00000000-0000-0000-0000-000000000b01', '00000000-0000-0000-0000-000000000a01', 'fingerprint', '2026-01-01 11:10:00+00', 'error', 'failed', 'nas_timeout: [WinError 121] timed out', '{"cause":"nas_timeout"}'),
 (19, 'library','00000000-0000-0000-0000-000000000a02','00000000-0000-0000-0000-000000000a02', 'youtube_sync','2026-01-01 11:15:00+00', 'error', 'failed', 'youtube_bot_check: Sign in to confirm you are not a bot', '{"cause":"youtube_bot_check"}'),
 (14, 'catalog','00000000-0000-0000-0000-00000000ca7a',NULL,                                    'write_channels','2026-01-01 11:20:00+00','info', 'review', 'write_channels: 3 added awaiting review', '{"changes":{"kind":"write_channels","counts":{"added":3,"removed":0,"changed":0,"reordered":0}}}');

INSERT INTO events (channel, type, payload, created_at) VALUES
 ('jobs', 'job.started',  '{"job":{"id":6,"stage":"fingerprint","state":"running","subject_kind":"video","subject_id":"00000000-0000-0000-0000-000000000b06","subject_label":"Drama 2","library_id":"00000000-0000-0000-0000-000000000a02","priority":100,"attempts":1,"max_attempts":3,"outcome":null,"error_cause":null,"created_at":"2026-01-01T11:30:00Z","started_at":"2026-01-01T11:58:05Z","finished_at":null,"progress":null}}', '2026-01-01 11:58:05+00'),
 ('jobs', 'job.finished', '{"job":{"id":9,"stage":"thumb_hash","state":"done","subject_kind":"video","subject_id":"00000000-0000-0000-0000-000000000b01","subject_label":"Episode 1","library_id":"00000000-0000-0000-0000-000000000a01","priority":100,"attempts":1,"max_attempts":3,"outcome":"changed","error_cause":null,"created_at":"2026-01-01T10:59:00Z","started_at":"2026-01-01T11:00:00Z","finished_at":"2026-01-01T11:00:02Z","progress":null},"duration_ms":2000,"triggered":[{"stage":"detect","count":1}]}', '2026-01-01 11:00:02+00'),
 ('jobs', 'job.failed',   '{"job":{"id":16,"stage":"fingerprint","state":"failed","subject_kind":"video","subject_id":"00000000-0000-0000-0000-000000000b01","subject_label":"Episode 1","library_id":"00000000-0000-0000-0000-000000000a01","priority":100,"attempts":3,"max_attempts":3,"outcome":null,"error_cause":"nas_timeout","created_at":"2026-01-01T10:50:00Z","started_at":"2026-01-01T11:00:00Z","finished_at":"2026-01-01T11:10:00Z","progress":null},"message":"[WinError 121] timed out","will_retry":false,"not_before":null}', '2026-01-01 11:10:00+00');

-- 30 one-minute samples per metric ending 12:00 (deterministic ramps)
INSERT INTO health_samples (at, host, metric, value)
SELECT '2026-01-01 12:00:00+00'::timestamptz - (g || ' minutes')::interval, 'PC', m.metric,
       m.base + (g % 7) * m.step
  FROM generate_series(0, 29) AS g,
       (VALUES ('nas.open_ms_p50', 40.0, 3.0), ('nas.open_ms_p95', 120.0, 15.0), ('nas.timeouts', 0.0, 0.0),
               ('nas.reads_busy', 1.0, 0.0), ('cpu.pct', 20.0, 4.0)) AS m(metric, base, step);

INSERT INTO workers (id, host, stages, pools, path_map, version, started_at, last_seen_at) VALUES
 ('PC/100/aaaa',  'PC',  ARRAY['scan','probe','thumb_hash','link','fingerprint','style_sample','youtube_sync','detect','fp_gate','tag','rate_style','plan_channels','write_channels'], ARRAY['nas_read','cpu','network_yt'], '{}', '0.0.1', '2026-01-01 08:00:00+00', '2026-01-01 11:59:58+00'),
 ('MAC/200/bbbb', 'MAC', ARRAY['extract','transcribe','subtitle_gen','index'], ARRAY['gpu','cpu'], '{"Y:/":"/Volumes/Y/"}', '0.0.1', '2026-01-01 08:30:00+00', '2026-01-01 11:59:50+00')
ON CONFLICT (id) DO NOTHING;

INSERT INTO extensions (name, enabled, settings) VALUES ('plex', false, '{"url":"","token":""}')
ON CONFLICT (name) DO NOTHING;

UPDATE worker_pools SET size = 2 WHERE pool = 'nas_read';
