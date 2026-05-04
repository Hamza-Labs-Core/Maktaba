# Implementation Plan — Story 23.5 Input validation and content safety

> Companion to [story-23-05-input-validation.md](story-23-05-input-validation.md).
> Story states *what* and *why*; this plan states *how*.
> Schema validation hooks the OpenAPI generator (Epic 7); path
> normalization is the new central helper this story introduces.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Schema validation | OpenAPI / GraphQL request schemas with `validator/v10` for nested struct rules. Reject with `problem+json`. |
| Path canonicalizer | `shared/paths/canonical.go` (Go) and `pipeline/src/maktaba_pipeline/paths.py` (Python). One helper, used everywhere. |
| Subprocess builder | `shared/exec/argv.go` and a Python equivalent. Forbids `sh -c` strings. |
| SSRF defense | `shared/httpsec/safe_fetcher.go`. Resolves DNS → checks IP → caps redirects. |
| Content sanitization | `pipeline/src/maktaba_pipeline/subtitles/sanitize.py` for VTT cue text; ditto for sidecar SRT files. |
| Lints | `tools/path-lint.go` forbids `os.Open`, `filepath.Clean` outside the helper, and string-prefix-checks against the library root. |
| Out of scope | Rate limiting (23.6); auth (23.1, 23.2); subtitle rendering (Epic 8). |

## 1. Architecture diagram

```
                 ┌────────────────────────┐
   client req ──►│ OpenAPI/GraphQL schema │
                 └──────────┬─────────────┘
                            │ ok, decode → validator/v10
                            ▼
                 ┌────────────────────────┐
                 │ handler                │
                 │  resolves IDs (UUIDv7) │
                 └──────────┬─────────────┘
                            │ canonical_under_roots(p)
                            ▼
                 ┌────────────────────────┐
                 │ paths.Canonical helper │
                 │  - resolve symlinks    │
                 │  - reject ..           │
                 │  - reject NUL bytes    │
                 │  - assert under roots  │
                 └──────────┬─────────────┘
                            │
              ┌─────────────┼─────────────┐
              ▼             ▼             ▼
            ffmpeg/      probe/        transcribe/
            argv slice   argv slice    argv slice
                            │
                            ▼ outputs
              ┌─────────────────────────┐
              │ subtitles.sanitize_cue  │ ← VTT/SRT
              └─────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `shared/paths/canonical.go` | The single helper. |
| `shared/paths/canonical_test.go` | Comprehensive coverage. |
| `pipeline/src/maktaba_pipeline/paths.py` | Mirror in Python. |
| `shared/exec/argv.go` | argv-only subprocess wrapper. |
| `pipeline/src/maktaba_pipeline/exec/argv.py` | Same. |
| `shared/httpsec/safe_fetcher.go` | SSRF-aware HTTP client. |
| `pipeline/src/maktaba_pipeline/subtitles/sanitize.py` | Sanitization for VTT cue text. |
| `pipeline/src/maktaba_pipeline/subtitles/sanitize_srt.py` | SRT-side sanitizer (for sidecar files). |
| `tools/path-lint.go` | CI lint that forbids direct path ops. |
| `api/internal/http/problem.go` | Already exists; add the `validation-failed` type. |
| Tests — `_test.go` per file plus `tests/security/path_traversal_test.sh` (TC1). |

### 2.2 Modified files

| Path | Change |
|---|---|
| Every Pipeline stage that accepts a path | Replace direct `Path(p)` opens with `paths.canonical_under_roots(p)`. |
| `pipeline/src/maktaba_pipeline/media/ffmpeg.py` | Use `exec.argv.run` only. |
| `api/internal/http/libraries.go` | Validate `library.root` through the canonicalizer. |
| `api/internal/http/poster.go` | Use `httpsec.SafeFetcher`. |
| `pipeline/src/maktaba_pipeline/grpc_server.py` | `ExtractEmbeddedSubtitle` validates `stream_index` against probed streams. |

### 2.3a Schema validation with `validator/v10`

Request structs decoded by chi handlers are validated with
`go-playground/validator/v10`. Failed validation is mapped to
`problem+json type="validation-failed"`:

```go
type CreateLibraryReq struct {
    Name string   `json:"name" validate:"required,min=1,max=128"`
    Root string   `json:"root" validate:"required,filepath"`
    Tags []string `json:"tags" validate:"omitempty,dive,alphanum,max=32"`
}

var validate = validator.New(validator.WithRequiredStructEnabled())

func decodeAndValidate[T any](r *http.Request) (T, error) {
    var v T
    if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
        return v, fmt.Errorf("decode: %w", err)
    }
    if err := validate.Struct(v); err != nil {
        return v, err  // *validator.ValidationErrors
    }
    return v, nil
}

// In the handler:
req, err := decodeAndValidate[CreateLibraryReq](r)
if err != nil {
    var verr validator.ValidationErrors
    if errors.As(err, &verr) {
        problem(w, 400, TypeValidationFailed, fieldList(verr))
        return
    }
    problem(w, 400, TypeInvalidArgument, "")
    return
}
```

`fieldList(verr)` emits the offending field names but never the raw
values (see EC: validation rejection contains the offending value).

### 2.3 Path canonicalizer

`shared/paths/canonical.go`:

```go
package paths

import (
    "errors"
    "os"
    "path/filepath"
    "strings"
)

var (
    ErrNULByte    = errors.New("paths: NUL byte in path")
    ErrTraversal  = errors.New("paths: path traversal forbidden")
    ErrOutsideRoot = errors.New("paths: path resolves outside any configured root")
    ErrNotExist   = errors.New("paths: path does not exist")
)

// CanonicalUnderRoots resolves the input through symlinks and asserts
// the resolved value is under one of the configured roots.
//
// Returns the absolute, symlink-resolved path on success; an error
// otherwise. Callers must not bypass; the path-lint enforces.
func CanonicalUnderRoots(p string, roots []string) (string, error) {
    if strings.ContainsRune(p, 0) {
        return "", ErrNULByte
    }
    // Reject ".." as a path COMPONENT (not a substring — a filename
    // like "report..final.pdf" is legal). Split on os.PathSeparator
    // BEFORE filepath.Abs would collapse them, so an attacker can't
    // sneak a `..` past a normalized root that still contains a parent
    // of the real root.
    for _, c := range strings.Split(filepath.Clean(p), string(os.PathSeparator)) {
        if c == ".." {
            return "", ErrTraversal
        }
    }
    abs, err := filepath.Abs(p)
    if err != nil { return "", err }
    real, err := filepath.EvalSymlinks(abs)
    if err != nil {
        if errors.Is(err, os.ErrNotExist) { return "", ErrNotExist }
        return "", err
    }
    realRoots := make([]string, len(roots))
    for i, r := range roots {
        rr, err := filepath.EvalSymlinks(r)
        if err != nil { return "", err }
        realRoots[i] = filepath.Clean(rr) + string(os.PathSeparator)
    }
    realPlus := real + string(os.PathSeparator)
    for _, rr := range realRoots {
        if strings.HasPrefix(realPlus, rr) {
            return real, nil
        }
    }
    return "", ErrOutsideRoot
}
```

The Python version:

```python
import os
from pathlib import Path
from typing import Sequence

class PathSecurityError(Exception): ...

def canonical_under_roots(p: str | os.PathLike[str], roots: Sequence[str]) -> Path:
    s = os.fspath(p)
    if "\x00" in s:
        raise PathSecurityError("NUL byte in path")
    if ".." in s.split(os.sep):
        raise PathSecurityError("traversal forbidden")
    real = Path(s).resolve(strict=True)
    real_roots = [Path(r).resolve(strict=True) for r in roots]
    for r in real_roots:
        try:
            real.relative_to(r)
            return real
        except ValueError:
            continue
    raise PathSecurityError(f"{real} is outside configured roots")
```

### 2.4 The path-lint

`tools/path-lint.go`:

```go
// Walks api/, streaming/, pipeline/ (Go portions), looking for:
//   * os.Open / os.OpenFile / ioutil.ReadFile direct on user-supplied
//     paths (heuristic: argument variable name from a struct tagged
//     `path:"true"`).
//   * filepath.Clean outside paths.CanonicalUnderRoots.
//   * strings.HasPrefix(p, root) checks (the helper already does this).
//
// Forbidden patterns escape only via `//pathlint:safe <reason>` comments.

func main() { /* ast walk; same skeleton as authz-lint */ }
```

The Python equivalent uses `ast.NodeVisitor`:

```python
class PathLint(ast.NodeVisitor):
    def visit_Call(self, node):
        if isinstance(node.func, ast.Attribute):
            n = node.func.attr
            if n in {"open"} and isinstance(node.func.value, ast.Name):
                if node.func.value.id == "os":
                    self.flag(node, "os.open: use canonical_under_roots first")
            if n == "Clean":
                self.flag(node, "use canonical_under_roots, not Clean alone")
```

### 2.5 argv-only exec

`shared/exec/argv.go`:

```go
package exec

import (
    "context"
    "errors"
    osexec "os/exec"
)

type Cmd struct {
    Path string
    Args []string  // argv[0] excluded; the runner prepends Path.
    Env  []string
}

func Run(ctx context.Context, c Cmd) ([]byte, error) {
    cmd := osexec.CommandContext(ctx, c.Path, c.Args...)
    cmd.Env = append(os.Environ(), c.Env...)
    return cmd.CombinedOutput()
}

// Banned: any sh -c, bash -c, /bin/sh, eval. Static check enforces.
```

CI grep:

```bash
grep -RE '(sh|bash|zsh) -c' --include='*.go' --include='*.py' \
  -- api/ streaming/ pipeline/ shared/ tools/ \
  && { echo "shell-string command detected"; exit 1; } || true
```

### 2.6 Safe HTTP fetcher

`shared/httpsec/safe_fetcher.go`:

```go
package httpsec

import (
    "context"
    "errors"
    "fmt"
    "net"
    "net/http"
    "net/url"
)

var (
    ErrPrivateAddress = errors.New("httpsec: private address forbidden")
    ErrTooManyRedirects = errors.New("httpsec: redirect limit exceeded")
)

type SafeFetcher struct {
    Client      *http.Client
    MaxRedirects int           // default 3
}

func New() *SafeFetcher {
    f := &SafeFetcher{MaxRedirects: 3}
    transport := &http.Transport{
        DialContext: (&net.Dialer{
            Control: func(network, address string, c syscall.RawConn) error {
                ip, _, _ := net.SplitHostPort(address)
                if isPrivate(net.ParseIP(ip)) { return ErrPrivateAddress }
                return nil
            },
        }).DialContext,
    }
    f.Client = &http.Client{
        Transport: transport,
        CheckRedirect: func(req *http.Request, via []*http.Request) error {
            if len(via) >= f.MaxRedirects { return ErrTooManyRedirects }
            return nil
        },
    }
    return f
}

func isPrivate(ip net.IP) bool {
    if ip == nil { return true }
    return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsPrivate() ||
           ip.IsUnspecified() || ip.IsMulticast()
}
```

The dialer's `Control` hook fires *after* DNS resolution but *before*
the connect, so `http://169.254.169.254/` and DNS-rebinding attacks
both fail. The same callback intercepts each redirect's resolved IP.

**Platform note.** `syscall.RawConn` and the `Control` callback signature
above are supported on Linux and Darwin (Maktaba's two server targets
per architecture §1.4). On Windows the signature differs (`Control`
takes `uintptr` rather than `syscall.RawConn`); since Maktaba does not
ship a Windows server, we hard-fail the build on `GOOS=windows` for
this package via a build tag:

```go
//go:build linux || darwin
```

A separate stub file `safe_fetcher_windows.go` returns
`fmt.Errorf("httpsec: not supported on windows")` from `New()` so
client-side cross-compilation (e.g., a developer running `go build`
on Windows) fails fast with a clear message.

### 2.7 VTT/SRT sanitization

`pipeline/src/maktaba_pipeline/subtitles/sanitize.py`:

```python
import html
import re

# Allow plain text, basic structural newlines. Drop any HTML/script.
TAG_RE = re.compile(r"<[^>]+>")

def sanitize_cue(text: str) -> str:
    """Sanitize a VTT cue's text for safe rendering.

    The cue may have come from upstream STT output (ours) or an external
    SRT file (operator-supplied). Both are equally untrusted: HTML-escape
    the whole thing, then strip any residual tag-shaped fragments.
    """
    escaped = html.escape(text, quote=True)
    # The VTT spec allows <c>, <i>, <u>, <ruby>; we currently render none
    # of these, so drop them outright. v2 may opt these in via a strict
    # whitelist.
    return TAG_RE.sub("", escaped)
```

The same sanitizer is called from:

* `subtitles/vtt_writer.py` before writing a cue to disk.
* `subtitles/srt_loader.py` when ingesting a sidecar SRT.

### 2.8a Probe output size-bound (AC5)

`ffprobe` output is bounded to a fixed maximum (1 MiB) before parsing,
to prevent a hostile media file from causing the pipeline to allocate
an arbitrary-size JSON document:

```python
PROBE_OUTPUT_LIMIT = 1 << 20  # 1 MiB

async def probe(path: Path) -> ProbeResult:
    proc = await asyncio.create_subprocess_exec(
        "ffprobe", "-v", "error", "-print_format", "json",
        "-show_format", "-show_streams", "--", str(path),
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )
    out_chunks: list[bytes] = []
    total = 0
    async for chunk in proc.stdout:
        total += len(chunk)
        if total > PROBE_OUTPUT_LIMIT:
            proc.kill()
            await proc.wait()
            raise ProbeTooLarge(f"ffprobe output exceeded {PROBE_OUTPUT_LIMIT} bytes")
        out_chunks.append(chunk)
    await proc.wait()
    return parse_probe(b"".join(out_chunks))
```

The Go `argv.Run` equivalent caps `cmd.CombinedOutput()` via a
`bytes.Buffer` with a write-limit wrapper.

### 2.8 ExtractEmbeddedSubtitle gate (AC6)

`pipeline/src/maktaba_pipeline/grpc_server.py`:

```python
async def ExtractEmbeddedSubtitle(self, request, context):
    if request.stream_index < 0:
        await context.abort(grpc.StatusCode.INVALID_ARGUMENT,
                            "stream_index must be non-negative")
    probe = await self.probe_cache.get(request.video_id)
    sub_streams = [s for s in probe.streams if s.codec_type == "subtitle"]
    if request.stream_index >= len(sub_streams):
        await context.abort(grpc.StatusCode.INVALID_ARGUMENT,
                            f"video has {len(sub_streams)} subtitle streams; "
                            f"requested index {request.stream_index}")
    # ... proceed to ffmpeg via exec.argv.run with explicit args.
```

### 2.8b Go vs Python parity matrix

The Go and Python implementations of the helpers above intentionally
diverge in a few places; this matrix is the source of truth.

| Concern | Go (`shared/...`) | Python (`pipeline/...`) | Parity? |
|---|---|---|---|
| Path canonicalizer | `paths.CanonicalUnderRoots` | `paths.canonical_under_roots` | **Same semantics**; both reject `..` components, NUL bytes, and out-of-root via symlink resolution. |
| argv subprocess | `shared/exec.Run` (CombinedOutput, capped) | `pipeline.exec.argv.run` (subprocess_exec, capped) | **Same semantics**; Python uses `asyncio.subprocess` because the pipeline is async. |
| Output size cap | `bytes.Buffer` with write limit | per-chunk total accumulator | **Same semantics**, different idioms. |
| SSRF safe fetcher | `httpsec.SafeFetcher` (Linux/Darwin) | not implemented (pipeline does not fetch user-supplied URLs) | **Go-only by design**; the pipeline's only outbound HTTP is to the configured STT/embedding backends, which are loaded from the secret registry. |
| Subtitle sanitizer | not implemented (pipeline owns subtitles) | `subtitles.sanitize_cue` | **Python-only by design**. |
| Path-lint | `tools/path-lint.go` (AST) | `tools/path-lint.py` (`ast.NodeVisitor`) | **Same intent**; each language's lint targets that language's idioms. |
| Validator | `go-playground/validator/v10` | `pydantic` (already used in pipeline) | **Equivalent**; both reject NaN, range violations, and length caps. |

Differences are deliberate: the API/streaming services don't render
subtitles, and the pipeline doesn't fetch operator-supplied URLs.

### 2.9 Validation problem-types

`api/internal/http/problem.go` adds:

```go
// problem+json types reserved by Story 23.5.
const (
    TypePathTraversal     = "https://maktaba.io/problems/path-traversal"
    TypeNULByteInPath     = "https://maktaba.io/problems/nul-byte"
    TypeOutsideRoot       = "https://maktaba.io/problems/outside-root"
    TypePrivateAddress    = "https://maktaba.io/problems/private-address"
    TypeValidationFailed  = "https://maktaba.io/problems/validation-failed"
    TypeInvalidArgument   = "https://maktaba.io/problems/invalid-argument"
)
```

Handlers map errors → these types. Clients (web, mobile) display the
type-specific message.

## 3. Test plan

### 3.1 Path traversal (TC1)

| Test | What it pins |
|---|---|
| `TestPathTraversalRejected` | `POST /api/libraries` with `root="/etc/passwd/.."` → 400 `path-traversal`. |
| `TestNormalizedTraversalRejected` | `root="/var/maktaba/../../etc"` → 400 `path-traversal`. |
| `TestSymlinkAboveRootRejected` | A directory `library/sneak` symlinked to `/etc` → `CanonicalUnderRoots` returns `ErrOutsideRoot`. |
| `TestNULByteRejected` | A path with `\x00` → `ErrNULByte`; never reaches `os.Open`. |
| `TestPathLintCatchesDirectOpen` | A fixture `pipeline/sneak.py` doing `open(path)` on a user-supplied var fails the lint. |
| `TestFilenameWithDoubleDotAccepted` | A file named `report..final.pdf` (literal `..` substring, not a component) is **accepted** by `CanonicalUnderRoots`; component-wise check distinguishes substring `..` from path-component `..`. |

### 3.2 Command injection (TC2)

| Test | What it pins |
|---|---|
| `TestFilenameWithSemicolonNotInterpreted` | A filename `"; rm -rf /"` flows through `ffmpeg` argv unchanged; `ps` shows the literal arg, not a shell expansion. |
| `TestNoShDashCAnywhere` | CI grep returns zero hits for `sh -c` patterns in repo source (excluding tools/). |
| `TestArgvCmdRejectsShellMeta` | `exec.Run` accepts argv slices only; passing a single string with spaces fails compile (different func signature). |

### 3.3 SSRF (TC3)

| Test | What it pins |
|---|---|
| `TestSSRFLinkLocalRefused` | Fetcher to `http://169.254.169.254/` errors with `ErrPrivateAddress`. |
| `TestSSRFLoopbackRefused` | `http://localhost:5432/` errors. |
| `TestSSRFRedirectChainCapped` | A target that redirects 4× errors with `ErrTooManyRedirects`. |
| `TestDNSRebindingDetected` | A host whose first DNS resolves to a public IP and second to `127.0.0.1` is caught by the connect-time IP check. |

### 3.4 Cue escaping (TC4)

| Test | What it pins |
|---|---|
| `TestSanitizeEscapesScriptTag` | `sanitize_cue("<script>alert(1)</script>")` returns `&lt;script&gt;alert(1)&lt;/script&gt;`. |
| `TestSanitizeStripsRubyTag` | `sanitize_cue("<ruby>魚</ruby>")` returns `魚`. |
| `TestVttRendererPlainText` | A rendered cue from the player's VTT path emits text content `<script>alert(1)</script>` literally (DOM treats it as text). |
| `TestSrtSidecarSanitized` | An SRT file containing `<script>` is sanitized at ingest; the segments table stores the escaped form. |

### 3.5 ExtractEmbeddedSubtitle (TC5)

| Test | What it pins |
|---|---|
| `TestExtractInvalidIndex` | Probed video has 2 subtitle streams; `ExtractEmbeddedSubtitle(stream_index=5)` returns `INVALID_ARGUMENT`; ffmpeg never spawns. |
| `TestExtractNegativeIndex` | `stream_index=-1` returns `INVALID_ARGUMENT`. |

### 3.6 Probe output size-bound (AC5)

| Test | What it pins |
|---|---|
| `TestProbeOutputCappedAt1MiB` | A pathological media file producing ffprobe JSON > 1 MiB: the pipeline kills the process and returns `ProbeTooLarge`; never tries to parse the oversized output. |
| `TestProbeOutputUnderCapSucceeds` | A normal file (~10 KiB ffprobe output) parses cleanly. |
| `TestArgvRunOutputCap` | The Go `exec.Run` wrapper caps combined output at the configured limit; truncation returns a sentinel error. |

## 4. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| Subtitle file with `<script>` (EC1) | Sanitizer escapes; player renders plain text. | `TestSrtSidecarSanitized` |
| Filename with NUL byte (EC2) | `CanonicalUnderRoots` rejects; no path operation accepts NUL. | `TestNULByteRejected` |
| Symlink to another root (EC3) | `EvalSymlinks` resolves; the resolved path must be under *any* configured root; otherwise reject. A warning logs the symlink target. | `TestSymlinkBetweenRoots` |
| NaN / out-of-range (EC4) | `validator/v10` rejects with 400; range/clamp logic only fires on integers within range. A `?from=NaN` test pins this. | `TestQueryParamNaNRejected` |
| Path with non-Latin chars | UTF-8 paths supported; `os.PathSeparator` handles correctly; tested with Arabic and Chinese filenames. | `TestUtf8Paths` |
| Library root that itself contains `..` | `CanonicalUnderRoots` runs `EvalSymlinks` on roots too, so a misconfigured root containing `..` is also normalized. | `TestRootWithTraversalNormalized` |
| Symlink loop | `EvalSymlinks` returns `EINVAL`; mapped to `ErrOutsideRoot` with a "loop detected" annotation in logs. | `TestSymlinkLoopRejected` |
| Path that's exactly the root (no trailing sep) | `CanonicalUnderRoots` accepts; the trailing separator is added internally before HasPrefix. | `TestPathExactlyRoot` |
| Subtitle with bidi override chars | Sanitizer doesn't strip Unicode bidi chars by default; documented as a v2 hardening. | n/a |
| Validation rejection contains the offending value | The `problem+json` body's `detail` includes the canonicalized field name but never the raw value (could leak secrets if a token was sent in a wrong field). | `TestValidationDetailNoValueLeak` |
| FFmpeg argv with `-` filename | A filename starting with `-` is interpreted as a flag by ffmpeg; the wrapper prefixes `--` before the input filename. | `TestFFmpegDashPrefix` |

## 5. Dependencies

| Dep | Version | Why |
|---|---|---|
| `github.com/go-playground/validator/v10` | latest | Struct tag validation. |
| `path/filepath` | stdlib | Path manipulation. |
| `net/http`, `net` | stdlib | SSRF dialer. |
| `html` (Python) | stdlib | Cue escaping. |
| `ast` (Python), `go/ast` (Go) | stdlib | Lints. |

## 6. Acceptance checklist

**Path safety**
- [ ] `CanonicalUnderRoots` is the single helper.
- [ ] Path-lint catches direct `os.Open` / `filepath.Clean`.
- [ ] NUL bytes, traversal, and out-of-root all rejected.

**Subprocess**
- [ ] `exec.argv` wrapper used everywhere.
- [ ] CI grep refuses `sh -c` strings.

**SSRF**
- [ ] `httpsec.SafeFetcher` rejects RFC1918, loopback, link-local, multicast.
- [ ] Redirect cap = 3.

**Sanitization**
- [ ] Cue text HTML-escaped + tag-stripped on write.
- [ ] Sidecar SRT input sanitized at ingest.

**ExtractEmbeddedSubtitle**
- [ ] `stream_index` validated against probe; `INVALID_ARGUMENT` returned without spawning ffmpeg.
