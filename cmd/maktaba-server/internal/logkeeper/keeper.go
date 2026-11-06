package logkeeper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config is the keeper's settings ([logs] in server.toml, config.LogsSection).
type Config struct {
	// Root is <data>/logs: spool/ and segments/ go under it.
	Root string
	// Node is this machine's name on every line ("home", or a joined machine's id).
	Node string
	// Retention keeps info and above, DebugRetention debug lines (7 days, 24 h).
	Retention, DebugRetention time.Duration
	// MaxSegments is the segments' share of the disk cap (550 MB of 1 GB).
	MaxSegments int64
	// MinFree: below it on the data volume, debug lines stop and the oldest hours go
	// (5 GB). Under HardFree (1 GB) only warn and above are written.
	MinFree, HardFree int64
	// Echo is server.log (the supervisor's stderr); EchoAll writes every line there as it
	// came (as before the keeper), else only warn and above, as kept lines.
	Echo    io.Writer
	EchoAll bool
	// Component names a supervised process's catalog component (ByProcess).
	Component func(role, instance string) string
	// Now and Free are the clock and the free bytes of a path (-1: unknown); nil: the
	// real ones.
	Now  func() time.Time
	Free func(path string) int64
	// Poll is how often a spool is read (0: 200 ms); SpoolMax the size a spool is
	// emptied at once it is read to its end (0: 32 MB).
	Poll     time.Duration
	SpoolMax int64
}

// Keeper is the log keeper of one machine.
type Keeper struct {
	cfg  Config
	segs *segments

	mu      sync.Mutex
	spools  map[string]*follower // by path
	dropped map[string]int       // since the last log_lines_dropped, by level
	freeAt  time.Time
	free    int64
	echoMu  sync.Mutex
	runCtx  context.Context
	// limMu guards the limits SetLimits changes while the keeper runs
	limMu sync.Mutex
}

// SetLimits changes retention and the segments' cap while the keeper runs (the admin's
// Retention page, story 53.11); the next janitor pass applies them. A zero leaves a value.
func (k *Keeper) SetLimits(retention, debugRetention time.Duration, maxSegments int64) {
	k.limMu.Lock()
	defer k.limMu.Unlock()
	if retention > 0 {
		k.cfg.Retention = retention
	}
	if debugRetention > 0 {
		k.cfg.DebugRetention = debugRetention
	}
	if maxSegments > 0 {
		k.cfg.MaxSegments = maxSegments
	}
}

func (k *Keeper) limits() (retention, debugRetention time.Duration, maxSegments int64) {
	k.limMu.Lock()
	defer k.limMu.Unlock()
	return k.cfg.Retention, k.cfg.DebugRetention, k.cfg.MaxSegments
}

// New makes a keeper; Run starts its followers' polling, the syncs and the janitor.
func New(cfg Config) *Keeper {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Free == nil {
		cfg.Free = FreeBytes
	}
	if cfg.Poll <= 0 {
		cfg.Poll = 200 * time.Millisecond
	}
	if cfg.SpoolMax <= 0 {
		cfg.SpoolMax = 32 << 20
	}
	if cfg.Retention <= 0 {
		cfg.Retention = 7 * 24 * time.Hour
	}
	if cfg.DebugRetention <= 0 {
		cfg.DebugRetention = 24 * time.Hour
	}
	if cfg.MaxSegments <= 0 {
		cfg.MaxSegments = 550 << 20
	}
	if cfg.HardFree <= 0 {
		cfg.HardFree = 1 << 30
	}
	if cfg.Component == nil {
		cfg.Component = func(string, string) string { return "" }
	}
	return &Keeper{cfg: cfg, segs: newSegments(cfg.Root, cfg.Now), spools: map[string]*follower{}, dropped: map[string]int{}, free: -1}
}

// Root is where the keeper writes.
func (k *Keeper) Root() string { return k.cfg.Root }

// ServiceName is a supervised process's service (and segment directory): its role, with
// its instance after a dot (pipeline.stt-0, tv-slot.blue).
func ServiceName(role, instance string) string {
	if instance == "" {
		return role
	}
	return role + "." + instance
}

// Spool is the file a supervised process writes its stdout and stderr to (append-only);
// the keeper follows it from where it last read, across restarts of the process and of
// the supervisor.
func (k *Keeper) Spool(role, instance string) (*os.File, error) {
	svc := ServiceName(role, instance)
	dir := filepath.Join(k.cfg.Root, "spool")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, safeName(svc)+".out")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	k.follow(path, Origin{Service: role, Instance: instance, Component: k.cfg.Component(role, instance)}, true)
	return f, nil
}

// FollowFile follows a log file another program writes (the desktop app's app.log, the
// launcher's): its new lines become the service's segments. It is never emptied.
func (k *Keeper) FollowFile(path string, o Origin) { k.follow(path, o, false) }

func (k *Keeper) follow(path string, o Origin, own bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.spools[path]; ok {
		return
	}
	f := &follower{k: k, path: path, origin: o, own: own}
	f.loadOffset()
	k.spools[path] = f
	if k.runCtx != nil {
		go f.run(k.runCtx)
	}
}

// Ingest keeps one line written by the supervisor itself: it is already in server.log, so
// it is never echoed there.
func (k *Keeper) Ingest(o Origin, raw []byte) { k.keep(o, raw, nil, false) }

// keep normalizes, gates and writes one line; echo is the original to copy to server.log
// (when mayEcho).
func (k *Keeper) keep(o Origin, raw []byte, echo []byte, mayEcho bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return
	}
	// Epic 54 (story 54.09 §3): the plugin host writes each plugin's lines as service
	// plugin.<id>; they get segments of their own and the plugin's component, so
	// retention and search work per plugin as for any component
	seg := o.Service + instanceSuffix(o.Instance)
	if o.Service == "plugins" {
		var head struct {
			Service string `json:"service"`
		}
		if json.Unmarshal(raw, &head) == nil {
			if id, ok := pluginService(o.Service, head.Service); ok {
				seg, o.Component = id, id
			}
		}
	}
	l := Normalize(raw, o, k.cfg.Node, k.cfg.Now())
	switch {
	case !mayEcho:
	case k.cfg.EchoAll:
		k.echo(echo)
	case Rank(l.Level) >= Rank("warn"):
		k.echo(l.JSON)
	}
	if floor := k.floor(); Rank(l.Level) < floor {
		k.mu.Lock()
		k.dropped[l.Level]++
		k.mu.Unlock()
		return
	}
	comp := o.Component
	var probe struct {
		Component string `json:"component"`
	}
	_ = json.Unmarshal(l.JSON, &probe)
	if probe.Component != "" {
		comp = probe.Component
	}
	if err := k.segs.write(seg, l, comp); err != nil {
		k.mu.Lock()
		k.dropped[l.Level]++
		k.mu.Unlock()
	}
}

func instanceSuffix(instance string) string {
	if instance == "" {
		return ""
	}
	return "." + instance
}

func (k *Keeper) echo(b []byte) {
	if k.cfg.Echo == nil {
		return
	}
	k.echoMu.Lock()
	defer k.echoMu.Unlock()
	_, _ = k.cfg.Echo.Write(append(bytes.TrimRight(b, "\r\n"), '\n'))
}

// floor is the lowest level written now: debug normally; info under MinFree free; warn
// under HardFree. The free space is read at most every 30 s.
func (k *Keeper) floor() int {
	now := k.cfg.Now()
	k.mu.Lock()
	if now.Sub(k.freeAt) > 30*time.Second || k.freeAt.IsZero() {
		k.freeAt = now
		k.mu.Unlock()
		free := k.cfg.Free(k.cfg.Root)
		k.mu.Lock()
		k.free = free
	}
	free := k.free
	k.mu.Unlock()
	switch {
	case free < 0:
		return Rank("debug")
	case free < k.cfg.HardFree:
		return Rank("warn")
	case free < k.cfg.MinFree:
		return Rank("info")
	}
	return Rank("debug")
}

// LowDisk reports whether lines are being reduced for lack of disk ("Low disk: logs
// reduced" on System health).
func (k *Keeper) LowDisk() bool { return k.floor() > Rank("debug") }

// Run reads the spools, syncs the segments every second, says how many lines were
// dropped once a minute while it lasts, and runs the janitor every 5 minutes, until ctx
// ends; then it reads what is left and closes the files.
func (k *Keeper) Run(ctx context.Context) {
	k.mu.Lock()
	k.runCtx = ctx
	fs := make([]*follower, 0, len(k.spools))
	for _, f := range k.spools {
		fs = append(fs, f)
	}
	k.mu.Unlock()
	var wg sync.WaitGroup
	for _, f := range fs {
		wg.Add(1)
		go func(f *follower) { defer wg.Done(); f.run(ctx) }(f)
	}
	k.Janitor()
	sync := time.NewTicker(time.Second)
	minute := time.NewTicker(time.Minute)
	jan := time.NewTicker(5 * time.Minute)
	defer sync.Stop()
	defer minute.Stop()
	defer jan.Stop()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			k.segs.close()
			return
		case <-sync.C:
			k.segs.sync()
		case <-minute.C:
			k.reportDropped()
		case <-jan.C:
			k.Janitor()
		}
	}
}

// Flush reads every spool to its end and syncs the segments now (tests, and a shutdown).
func (k *Keeper) Flush() {
	k.mu.Lock()
	fs := make([]*follower, 0, len(k.spools))
	for _, f := range k.spools {
		fs = append(fs, f)
	}
	k.mu.Unlock()
	for _, f := range fs {
		f.poll()
		f.mu.Lock()
		f.saveOffset()
		f.mu.Unlock()
	}
	k.reportDropped()
	k.segs.sync()
}

// Close flushes and closes the open segments.
func (k *Keeper) Close() {
	k.Flush()
	k.segs.close()
}

func (k *Keeper) reportDropped() {
	k.mu.Lock()
	d := k.dropped
	k.dropped = map[string]int{}
	k.mu.Unlock()
	if len(d) == 0 {
		return
	}
	attrs := map[string]any{}
	total := 0
	for lv, n := range d {
		attrs[lv] = n
		total += n
	}
	attrs["total"] = total
	b, _ := json.Marshal(map[string]any{"level": "warn", "msg": "log_lines_dropped", "attrs": attrs})
	l := Normalize(b, Origin{Service: "supervisor", Component: ""}, k.cfg.Node, k.cfg.Now())
	_ = k.segs.write("supervisor", l, "")
	k.echo(l.JSON)
}

// follower reads one file from its offset.
type follower struct {
	k       *Keeper
	path    string
	origin  Origin
	own     bool
	mu      sync.Mutex
	offset  int64
	partial []byte
	savedAt time.Time
}

func (f *follower) offsetPath() string { return f.path + ".offset" }

func (f *follower) loadOffset() {
	if !f.own {
		// another program's file: from its end (its history is not this keeper's)
		if fi, err := os.Stat(f.path); err == nil {
			f.offset = fi.Size()
		}
		return
	}
	if b, err := os.ReadFile(f.offsetPath()); err == nil {
		if n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil && n >= 0 {
			f.offset = n
		}
	}
}

func (f *follower) saveOffset() {
	if !f.own {
		return
	}
	_ = os.WriteFile(f.offsetPath(), []byte(strconv.FormatInt(f.offset, 10)), 0o600)
}

func (f *follower) run(ctx context.Context) {
	t := time.NewTicker(f.k.cfg.Poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			f.poll()
			f.mu.Lock()
			f.saveOffset()
			f.mu.Unlock()
			return
		case <-t.C:
			f.poll()
		}
	}
}

// poll reads what was appended since the last read and keeps each whole line; a spool
// read to its end past SpoolMax is emptied (the next write lands at its start).
func (f *follower) poll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	fh, err := os.Open(f.path)
	if err != nil {
		return
	}
	defer fh.Close()
	fi, err := fh.Stat()
	if err != nil {
		return
	}
	if fi.Size() < f.offset {
		// emptied or replaced (a rotation): from its start
		f.offset, f.partial = 0, nil
	}
	if fi.Size() > f.offset {
		if _, err := fh.Seek(f.offset, io.SeekStart); err != nil {
			return
		}
		buf := make([]byte, 256<<10)
		for {
			n, err := fh.Read(buf)
			if n > 0 {
				f.offset += int64(n)
				f.consume(buf[:n])
			}
			if err != nil {
				break
			}
		}
	}
	if f.own && f.offset >= f.k.cfg.SpoolMax && len(f.partial) == 0 {
		if fi2, err := os.Stat(f.path); err == nil && fi2.Size() == f.offset {
			if os.Truncate(f.path, 0) == nil {
				f.offset = 0
			}
		}
	}
	if now := f.k.cfg.Now(); now.Sub(f.savedAt) >= time.Second {
		f.saveOffset()
		f.savedAt = now
	}
}

func (f *follower) consume(b []byte) {
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			f.partial = append(f.partial, b...)
			if len(f.partial) > MaxLine {
				// a line longer than kept: what came so far is a line of its own
				f.line(f.partial)
				f.partial = nil
			}
			return
		}
		line := b[:i]
		if len(f.partial) > 0 {
			line = append(f.partial, line...)
			f.partial = nil
		}
		f.line(line)
		b = b[i+1:]
	}
}

func (f *follower) line(b []byte) {
	cp := append([]byte(nil), b...)
	f.k.keep(f.origin, cp, cp, f.own)
}

// segmentFile is one file the janitor and the reader see.
type segmentFile struct {
	path    string
	service string
	kind    string // "", "debug", "warn"
	hour    time.Time
	size    int64
}

// files lists every segment file (not the .idx), oldest hour first.
func (k *Keeper) files() []segmentFile {
	root := filepath.Join(k.cfg.Root, "segments")
	var out []segmentFile
	svcs, _ := os.ReadDir(root)
	for _, s := range svcs {
		if !s.IsDir() {
			continue
		}
		for _, kind := range []string{"", "debug", "warn"} {
			dir := filepath.Join(root, s.Name(), kind)
			es, _ := os.ReadDir(dir)
			for _, e := range es {
				name := e.Name()
				if e.IsDir() || (!strings.HasSuffix(name, ".ndjson") && !strings.HasSuffix(name, ".ndjson"+Zst)) {
					continue
				}
				h, err := time.Parse("20060102-15", name[:min(len(name), 11)])
				if err != nil {
					continue
				}
				fi, err := e.Info()
				if err != nil {
					continue
				}
				out = append(out, segmentFile{path: filepath.Join(dir, name), service: s.Name(), kind: kind, hour: h, size: fi.Size()})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].hour.Equal(out[j].hour) {
			return out[i].hour.Before(out[j].hour)
		}
		return out[i].path < out[j].path
	})
	return out
}

// Janitor applies retention and the disk cap: debug files older than DebugRetention and
// others older than Retention go; then, while the segments pass MaxSegments (or the
// volume is under MinFree), the oldest hours go first, across services, except each
// service's newest 24 h of warn and above (warn/), which go last.
func (k *Keeper) Janitor() {
	now := k.cfg.Now().UTC()
	retention, debugRetention, maxSegments := k.limits()
	files := k.files()
	var keep []segmentFile
	for _, f := range files {
		age := now.Sub(f.hour.Add(time.Hour))
		limit := retention
		if f.kind == "debug" {
			limit = debugRetention
		}
		if age > limit && !k.segs.isOpen(f.path) {
			k.remove(f)
			continue
		}
		keep = append(keep, f)
	}
	var total int64
	for _, f := range keep {
		total += f.size
	}
	over := func() bool {
		if total > maxSegments {
			return true
		}
		free := k.cfg.Free(k.cfg.Root)
		return free >= 0 && k.cfg.MinFree > 0 && free < k.cfg.MinFree
	}
	if !over() {
		return
	}
	// the floor last: each service's newest 24 h of warn+ lines
	floor := func(f segmentFile) bool { return f.kind == "warn" && now.Sub(f.hour) < 24*time.Hour }
	for pass := 0; pass < 2 && over(); pass++ {
		for _, f := range keep {
			if !over() {
				break
			}
			if (pass == 0 && floor(f)) || k.segs.isOpen(f.path) || f.size < 0 {
				continue
			}
			k.remove(f)
			total -= f.size
			f.size = -1
		}
		// the files already gone are marked: the second pass sees only the floor
		next := keep[:0]
		for _, f := range keep {
			if f.size >= 0 {
				next = append(next, f)
			}
		}
		keep = next
	}
}

func (k *Keeper) remove(f segmentFile) {
	_ = os.Remove(f.path)
	_ = os.Remove(idxPath(f.path))
}

// String is for logs and tests.
func (k *Keeper) String() string { return fmt.Sprintf("logkeeper(%s)", k.cfg.Root) }

// pluginService is a plugin's segment directory: a line of the plugin host (role plugins)
// whose service is plugin.<id> with an id of plugin id characters.
func pluginService(role, service string) (string, bool) {
	id, ok := strings.CutPrefix(service, "plugin.")
	if role != "plugins" || !ok || id == "" || len(id) > 100 {
		return "", false
	}
	for _, c := range id {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '.' && c != '-' {
			return "", false
		}
	}
	if strings.Contains(id, "..") || strings.HasPrefix(id, ".") {
		return "", false
	}
	return service, true
}
