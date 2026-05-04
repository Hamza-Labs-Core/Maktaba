# Implementation Plan — Story 21.1 Structured Logging

> Companion to [story-21-01-structured-logging.md](story-21-01-structured-logging.md).
> One global logger per service; JSON in prod; required base fields; runtime level toggle;
> static lint over call sites; FFmpeg stderr wrapped.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Go | `log/slog` standard library; JSON handler in prod, text in dev. |
| Python | `structlog` with JSON renderer in prod, console in dev. |
| TS | thin `logger` wrapper around `console` mapping field names. |
| Required base fields | `ts, level, service, msg`; contextual fields injected via context. |
| Level toggle | SIGUSR1 cycles unit (Go); admin endpoint `POST /admin/log/level`. |
| Lint | AST walker that flags `+`/`fmt.Sprintf` of user data into `msg`. |

## 1. Project layout

```
shared/log/
├── go/
│   ├── logger.go             # NewLogger, Default, set level
│   ├── handler.go            # JSON+text handler
│   ├── ctx.go                # WithRequestID, WithSessionID, ...
│   ├── ffmpeg.go             # FFmpeg stderr → structured wrap
│   ├── lint/
│   │   ├── concat_lint.go
│   │   └── concat_lint_test.go
│   └── logger_test.go
├── py/
│   ├── logger.py
│   ├── ctx.py
│   ├── ffmpeg_wrap.py
│   ├── lint/
│   │   └── concat_lint.py
│   └── tests/
└── ts/
    ├── logger.ts
    └── logger.test.ts
```

## 2. Go logger

```go
// shared/log/go/logger.go
package log

import (
    "log/slog"
    "os"
    "sync/atomic"
)

var (
    levelVar = new(slog.LevelVar)
    Default  *slog.Logger
)

func Init(service, env string) {
    levelVar.Set(slog.LevelInfo)
    var h slog.Handler
    if env == "prod" {
        h = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: levelVar, ReplaceAttr: replaceAttrs})
    } else {
        h = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: levelVar})
    }
    Default = slog.New(h).With("service", service)
    slog.SetDefault(Default)
    installSIGUSR1()
}

func replaceAttrs(groups []string, a slog.Attr) slog.Attr {
    if a.Key == slog.TimeKey {
        return slog.String("ts", a.Value.Time().UTC().Format(time.RFC3339Nano))
    }
    if a.Key == slog.LevelKey {
        return slog.String("level", strings.ToLower(a.Value.String()))
    }
    if a.Key == slog.MessageKey {
        v := a.Value.String()
        if len(v) > 60_000 { return slog.String("msg", v[:60_000]) }    // EC1: truncated outer; full in trace
    }
    return a
}

func SetLevel(l slog.Level) { levelVar.Set(l) }
```

SIGUSR1:

```go
// shared/log/go/sigusr1.go
//go:build !windows
func installSIGUSR1() {
    ch := make(chan os.Signal, 1)
    signal.Notify(ch, syscall.SIGUSR1)
    go func() {
        levels := []slog.Level{slog.LevelInfo, slog.LevelDebug, slog.LevelWarn}
        i := 0
        for range ch {
            i = (i + 1) % len(levels)
            levelVar.Set(levels[i])
            slog.Info("log level cycled", "new_level", levels[i].String())
        }
    }()
}
```

## 3. Context fields

```go
// shared/log/go/ctx.go
type fieldKey string

const (
    keyRequestID fieldKey = "request_id"
    keySessionID fieldKey = "session_id"
    keyJobID     fieldKey = "job_id"
    keyVideoID   fieldKey = "video_id"
    keyUserID    fieldKey = "user_id"
)

func WithRequestID(ctx context.Context, id string) context.Context { return context.WithValue(ctx, keyRequestID, id) }
// ...similar...

func From(ctx context.Context) *slog.Logger {
    l := Default
    if v, _ := ctx.Value(keyRequestID).(string); v != "" { l = l.With("request_id", v) }
    if v, _ := ctx.Value(keySessionID).(string); v != "" { l = l.With("session_id", v) }
    if v, _ := ctx.Value(keyJobID).(string);     v != "" { l = l.With("job_id",     v) }
    if v, _ := ctx.Value(keyVideoID).(string);   v != "" { l = l.With("video_id",   v) }
    if v, _ := ctx.Value(keyUserID).(string);    v != "" { l = l.With("user_id",    v) }
    return l
}
```

Usage:

```go
log.From(ctx).Info("video imported", "duration_s", v.Duration)
```

## 4. Python logger

```python
# shared/log/py/logger.py
import structlog, logging, sys, os, time

def init(service: str, env: str = "prod"):
    timestamper = structlog.processors.TimeStamper(fmt="iso", utc=True)
    procs = [
        structlog.contextvars.merge_contextvars,
        structlog.processors.add_log_level,
        timestamper,
        _truncate_msg,
        structlog.processors.StackInfoRenderer(),
        structlog.processors.format_exc_info,
    ]
    if env == "prod":
        procs.append(structlog.processors.JSONRenderer())
    else:
        procs.append(structlog.dev.ConsoleRenderer())
    structlog.configure(
        processors=procs,
        wrapper_class=structlog.make_filtering_bound_logger(logging.INFO),
        context_class=dict,
        logger_factory=structlog.PrintLoggerFactory(file=sys.stdout),
    )
    log = structlog.get_logger().bind(service=service)
    return log

def _truncate_msg(_logger, _name, event_dict):
    msg = event_dict.get("event", "")
    if isinstance(msg, str) and len(msg) > 60_000:
        event_dict["event"] = msg[:60_000]
        event_dict["truncated"] = True
    return event_dict
```

## 5. TS browser logger

```ts
// shared/log/ts/logger.ts
type Level = 'debug' | 'info' | 'warn' | 'error';
const order: Record<Level, number> = { debug: 10, info: 20, warn: 30, error: 40 };
let levelMin = order[(import.meta.env.VITE_LOG_LEVEL ?? 'info') as Level];

export function log(level: Level, msg: string, fields: Record<string, unknown> = {}) {
    if (order[level] < levelMin) return;
    const line = { ts: new Date().toISOString(), level, service: 'web', msg, ...fields };
    // eslint-disable-next-line no-console
    (console as any)[level](JSON.stringify(line));
}
```

## 6. FFmpeg stderr wrapping

```go
// shared/log/go/ffmpeg.go
func WrapFFmpegStderr(ctx context.Context, r io.Reader) {
    sc := bufio.NewScanner(r)
    for sc.Scan() {
        line := sc.Text()
        // Common FFmpeg level mapping: lines starting with [error], [warning].
        level := slog.LevelInfo
        switch {
        case strings.HasPrefix(line, "[error]"):    level = slog.LevelError
        case strings.HasPrefix(line, "[warning]"):  level = slog.LevelWarn
        }
        From(ctx).Log(ctx, level, "ffmpeg",
            "event", "ffmpeg_stderr",
            "line",  line,
        )
    }
}
```

EC2 mapping: every FFmpeg stderr line becomes a structured `event=ffmpeg_stderr` record with the raw line as a field.

## 7. Lint — banned `msg` concatenation

```go
// shared/log/go/lint/concat_lint.go
//go:build loglint

func main() {
    fset := token.NewFileSet()
    bad := 0
    pkgs, _ := packages.Load(&packages.Config{Mode: packages.NeedSyntax|packages.NeedTypes|packages.NeedTypesInfo}, "./...")
    for _, p := range pkgs {
        for _, f := range p.Syntax {
            ast.Inspect(f, func(n ast.Node) bool {
                call, ok := n.(*ast.CallExpr)
                if !ok { return true }
                sel, ok := call.Fun.(*ast.SelectorExpr); if !ok { return true }
                if !isLogCall(sel) { return true }
                if len(call.Args) == 0 { return true }
                arg0 := call.Args[0]
                if isStringConcat(arg0, p.TypesInfo) {
                    fmt.Fprintf(os.Stderr, "FAIL: %s — log msg uses string concatenation; put user data in a field\n", fset.Position(arg0.Pos()))
                    bad++
                }
                return true
            })
        }
    }
    if bad > 0 { os.Exit(1) }
}
```

`isLogCall` matches `slog.*`, `log.From(*)`, etc. `isStringConcat` looks for `*ast.BinaryExpr` of strings or `fmt.Sprintf` whose format includes `%s` and any non-constant arg.

Python equivalent: `concat_lint.py` walks AST for `log.info(...)`/`info("string" + var)` and flags.

## 8. Test cases

### TC1 — Schema lint
A test file containing `slog.Info("user " + name)` runs `make lint:log`; assert exit code ≠ 0; assert error names the file:line.

### TC2 — Round-trip
`logger_test.go` runs `Init("api", "prod")`, captures stdout, emits 100 lines with varied fields, parses each via `json.Unmarshal`, asserts presence of `ts`, `level`, `service`, `msg`.

### TC3 — Hot-reload
Process started at `info`. SIGUSR1 → cycles to `debug`. Test issues `slog.Debug("here")`; assert it appears in captured stdout. Cycle again to verify.

### EC1 — Truncation
Emit a 70 KiB `msg`. Captured line has `msg` length ≤ 60 KiB and `truncated: true`.

### EC2 — FFmpeg stderr
Drive `WrapFFmpegStderr` with sample FFmpeg output. Captured records have `event=ffmpeg_stderr` and original lines preserved in `line`.

### EC3 — RTL escape
Emit `slog.Info("rtl test", "title", "كتاب الفهرست")`. Decode JSON; assert exact byte equality of the title.

## 9. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 > 64 KiB line | story | truncate at 60 KiB; flag `truncated:true`. |
| EC2 FFmpeg stderr | story | `event=ffmpeg_stderr`. |
| EC3 RTL Unicode | story | UTF-8 JSON encoding; tested. |
| Logger init race | impl | `Init` runs once at process start; `Default` set before any goroutine logs. |
| Loss on shutdown | impl | JSON handler flushes per-line; OS buffers limit gone-bytes to ≤ 4 KiB. |

## 10. Configuration

```yaml
log:
  level: info
  format: json                  # or "text"
  fields:
    service: api                # injected at init
log_admin:
  endpoint: /admin/log/level    # POST {"level":"debug"}; Story 23 admin auth
```

## 11. Dependencies

- Story 21.2 (metrics — log-level distribution counter `log_lines_total{level}`).
- Story 21.3 (tracing — `trace_id` and `span_id` injected when active).
- Story 21.5 (error reporting reads `level=error` lines).
- Story 21.8 (privacy — redaction layer wraps the handler).
