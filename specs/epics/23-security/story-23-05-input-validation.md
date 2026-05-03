# Story 23.5 — Input validation and content safety

Every external input is validated; SSRF, path traversal, command
injection, and untrusted file content are explicitly defended.

## Acceptance criteria

- AC1. All API inputs are validated against the OpenAPI / GraphQL
  schema; rejected with `400` and a structured `problem+json` error.
- AC2. Filesystem paths from clients are forbidden as values; only
  opaque IDs (UUID v7) are accepted. Paths in config / library
  roots are normalized through a single helper
  (`paths.canonical_under_roots(p)`) that:
  - resolves symlinks,
  - rejects `..` traversal,
  - rejects NUL bytes,
  - asserts the resolved path is under one of the configured library
    roots.
  Every internal worker that accepts a filesystem path
  (scanner, hasher, probe, transcribe, subtitle extract, FFmpeg
  spawn) calls this helper. A CI lint forbids direct `os.Open`,
  `filepath.Clean` without the helper, or string-prefix checks
  against the library root.
- AC3. FFmpeg invocations build argv as a slice, never a shell
  string; subprocess execution is `os/exec` with explicit args, no
  `sh -c`. The same rule applies to `pyannote`, `whisper-cli`, etc.
- AC4. SSRF defense: any code path fetching from a URL (e.g., poster
  fetch, OAuth callbacks if added) checks the resolved IP is not
  RFC 1918 / loopback / link-local and follows ≤ 3 redirects.
- AC5. Untrusted file content: probe outputs are size-bounded; a
  malformed media file produces an error, not a panic; subtitle
  files are sanitized for HTML/script injection before rendering.
  This sanitization applies to **both** sidecar SRT files and
  Maktaba-generated VTT cues from transcript text — every cue is
  HTML-escaped before write, so even a transcript that picks up
  `<script>` from STT output (or an external SRT) is rendered as
  plain text.
- AC6. `Pipeline.ExtractEmbeddedSubtitle(video_id, stream_index)`
  validates that `stream_index` references an existing subtitle
  stream of the probed file before invoking ffmpeg, returning
  `INVALID_ARGUMENT` otherwise.

## Test cases

- TC1. Path traversal: `POST /api/libraries` with `root="/etc/passwd/.."`
  is rejected; `root="/var/maktaba/../../etc"` after normalization
  is rejected; a symlink under the root pointing to `/etc` is also
  rejected.
- TC2. Command injection: a video filename containing `; rm -rf /`
  passes through every FFmpeg invocation untouched and produces
  no shell expansion.
- TC3. SSRF: `POST /api/libraries/poster?url=http://169.254.169.254/`
  refuses to fetch; `http://localhost:5432/` refuses to fetch.
- TC4. Cue escaping: a transcript segment containing the literal
  string `<script>alert(1)</script>` produces a VTT cue with
  `&lt;script&gt;alert(1)&lt;/script&gt;`; rendered output in the
  player is plain text.
- TC5. ExtractEmbeddedSubtitle: an out-of-range `stream_index`
  returns `INVALID_ARGUMENT` and never spawns ffmpeg.

## Edge cases

- EC1. Subtitle file with `<script>` tags — sanitizer escapes; a
  rendered VTT cue is plain text in the player.
- EC2. Filename with NUL byte — rejected; no path operation accepts
  a NUL byte.
- EC3. Symlinks under media root — followed only if the target is
  also under a configured root; otherwise rejected with a logged
  warning.
- EC4. NaN / out-of-range numbers in query params (e.g.,
  `?from=NaN`) — the parser rejects with `400` (not silently clamped
  to 0); range/clamp logic only applies to in-range integers.
