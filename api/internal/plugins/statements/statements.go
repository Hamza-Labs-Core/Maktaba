// Package statements is the plugin API's registry of named SQL statements (Epic 54,
// story 54.24 §2): the only way Maktaba's own first-party plugins reach the database.
//
// A first-party plugin's code runs in maktaba-plugin-runner with no database login. Each
// statement it runs is registered, by name, from the release's own build (the first-party
// bundle the server embeds); a call names the statement and passes its arguments, never
// SQL text. A call is checked here before anything runs:
//
//  1. the invocation's plugin owns the statement (else 403 plugin_capability);
//  2. a read-only invocation (the fingerprint runner's) runs only read statements;
//  3. each of the statement's scope checks passes against the token: subject $N the job's
//     video (or a video of its library), library $N the job's library, settings $N the key
//     of a rules section the manifest lists, and any other kind (season $N, transcript $N,
//     …: resolve.go) an id of a row whose library (and video) a lookup finds in the job's
//     scope (else 403 plugin_scope). A write lists a check for every id that binds its
//     rows (README §15.12; the build works them out, tools/plugins/statements_build.py);
//  4. it runs in a transaction that is SET LOCAL ROLE maktaba_fp_<plugin> (a NOLOGIN role
//     granted only the manifest's db.read / db.write tables: Postgres refuses anything
//     else, mapped to 403 plugin_capability) with maktaba.origin set to
//     plugin:<id>@<version>, so every write is attributed;
//  5. rows go back as JSON with typed values (codec.go) and every call is counted in
//     maktaba_plugin_statement_calls_total{plugin,statement}.
//
// A transaction the plugin's code opens (its "async with db.transaction()") is held here,
// by id, for that invocation only; nested ones are savepoints.
package statements

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus"
)

// Refusal codes (the plugin API's: words in web/src/i18n as plugins.error.<code>).
const (
	CodeCapability = "plugin_capability"
	CodeScope      = "plugin_scope"
	CodeInvalid    = "plugin_invalid"
	CodeTx         = "plugin_tx"
	CodeDatabase   = "plugin_database"
)

// TxIdle is how long an open transaction may wait for its next call before it is rolled
// back (the plugin's runner died, say).
const TxIdle = 2 * time.Minute

// Statement is one registered statement (tools/plugins/statements_build.py).
type Statement struct {
	Name     string `json:"name"`
	Mode     string `json:"mode"`
	Scope    string `json:"scope"`
	ScopeArg int    `json:"scope_arg"`
	// Also are the checks after the first (a write whose rows several ids bind)
	Also   []Check  `json:"also,omitempty"`
	Reads  []string `json:"reads"`
	Writes []string `json:"writes"`
	Text   string   `json:"text"`
	SHA256 string   `json:"sha256"`
}

// Check is one scope check: a kind and the 1-based argument it reads.
type Check struct {
	Scope string `json:"scope"`
	Arg   int    `json:"arg"`
}

// Checks are every check the statement runs (none for catalog).
func (s *Statement) Checks() []Check {
	if s.Scope == "catalog" {
		return nil
	}
	return append([]Check{{Scope: s.Scope, Arg: s.ScopeArg}}, s.Also...)
}

// Scope is what a call's token allows: whose statements, which subject and library, and
// which settings sections.
type Scope struct {
	Invocation  string
	PluginID    string
	Version     string
	SubjectKind string
	SubjectID   string
	LibraryID   string
	ReadOnly    bool
	// Settings are the rules sections the manifest lists ([trusted] settings)
	Settings []string
}

// Error is a refused or failed call.
type Error struct {
	Status int
	Code   string
	Detail string
	// badConn: the connection was closed before the statement ran (driver.ErrBadConn) or
	// the database ended the session, rolling it back: a statement outside a transaction
	// is safe to run again.
	badConn bool
}

func (e *Error) Error() string { return e.Code + ": " + e.Detail }

func refuse(status int, code, detail string) *Error {
	return &Error{Status: status, Code: code, Detail: detail}
}

// Request is one statement call (trusted/stmt).
type Request struct {
	Name  string              `json:"name"`
	Op    string              `json:"op"` // fetch | execute | executemany
	Args  []json.RawMessage   `json:"args"`
	Rows  [][]json.RawMessage `json:"rows"`
	Tx    string              `json:"tx"`
	Limit int                 `json:"limit"`
}

// Response is what a call answers.
type Response struct {
	Columns []string `json:"columns,omitempty"`
	Rows    [][]any  `json:"rows,omitempty"`
	Status  string   `json:"status,omitempty"`
}

// TxRequest opens, ends or marks an open transaction (trusted/tx).
type TxRequest struct {
	Op string `json:"op"` // begin | commit | rollback | savepoint | release | rollback_to
	Tx string `json:"tx"`
	SP string `json:"sp"`
}

type openTx struct {
	tx    *sql.Tx
	conn  *sql.Conn
	inv   string
	begun time.Time
	// mu is held by the call using the transaction; done (under mu) once it is rolled back
	// or committed and its connection given back
	mu   sync.Mutex
	done bool
	// doomed: ended (its invocation, the reaper, Close) while possibly held; busy: a call
	// holds it; lastUsed: unix nanoseconds of the last call's start or end
	doomed   atomic.Bool
	busy     atomic.Bool
	lastUsed atomic.Int64
	readOnly bool
}

func (ot *openTx) touch(t time.Time)     { ot.lastUsed.Store(t.UnixNano()) }
func (ot *openTx) lastUsedAt() time.Time { return time.Unix(0, ot.lastUsed.Load()) }

// Executor runs registered statements for the plugin API. Its connections: pool.go.
type Executor struct {
	DB *sql.DB

	cfg       Config
	owned     bool          // DB is the executor's own (Open): Close closes it
	slots     chan struct{} // one per connection held (kept, in a call, or in a transaction)
	waiting   atomic.Int32
	stop      chan struct{}
	reaped    chan struct{}
	closeOnce sync.Once

	mu     sync.Mutex
	closed bool
	reg    map[string]map[string]*Statement
	txs    map[string]*openTx
	vlibs  map[string]map[string]string // invocation -> video -> library
	// idle holds connections already SET ROLE to a plugin's role with its origin, by
	// plugin@version: a statement on one is one round trip (story 54.24 N18)
	idle map[string][]parked
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

var (
	metricsOnce sync.Once
	callsTotal  *prometheus.CounterVec
	callSeconds *prometheus.HistogramVec
)

func metrics() {
	metricsOnce.Do(func() {
		callsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "maktaba_plugin_statement_calls_total",
			Help: "Named statements run for Maktaba's first-party plugins, by plugin and statement (Epic 54)."},
			[]string{"plugin", "statement"})
		callSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "maktaba_plugin_statement_seconds",
			Help:    "How long a first-party plugin's named statement took (Epic 54).",
			Buckets: []float64{.0005, .001, .002, .005, .01, .05, .25, 1, 5}}, []string{"plugin"})
		_ = prometheus.DefaultRegisterer.Register(callsTotal)
		_ = prometheus.DefaultRegisterer.Register(callSeconds)
	})
}

// Register sets a plugin's statements (replacing what it had).
func (e *Executor) Register(pluginID string, sts []Statement) error {
	m := map[string]*Statement{}
	for i := range sts {
		s := sts[i]
		if s.Mode != "read" && s.Mode != "write" {
			return fmt.Errorf("statements: %s: mode %q", s.Name, s.Mode)
		}
		if s.Scope == "catalog" && len(s.Also) > 0 {
			return fmt.Errorf("statements: %s: catalog with checks", s.Name)
		}
		for _, c := range s.Checks() {
			if !KnownScope(c.Scope) || c.Arg < 1 {
				return fmt.Errorf("statements: %s: scope %s $%d", s.Name, c.Scope, c.Arg)
			}
		}
		if _, dup := m[s.Name]; dup {
			return fmt.Errorf("statements: %s: two statements named %s", pluginID, s.Name)
		}
		m[s.Name] = &s
	}
	e.mu.Lock()
	e.reg[pluginID] = m
	e.mu.Unlock()
	return nil
}

// Lookup answers a plugin's statement by name.
func (e *Executor) Lookup(pluginID, name string) *Statement {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reg[pluginID][name]
}

// Role is the Postgres role a first-party plugin's statements run as.
func Role(pluginID string) string {
	short := strings.TrimPrefix(pluginID, "tv.maktaba.")
	return "maktaba_fp_" + strings.NewReplacer("-", "_", ".", "_").Replace(short)
}

func newID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return "tx_" + hex.EncodeToString(b)
}

// Stmt runs one call.
func (e *Executor) Stmt(ctx context.Context, sc Scope, req Request) (*Response, *Error) {
	metrics()
	st := e.Lookup(sc.PluginID, req.Name)
	if st == nil {
		return nil, refuse(403, CodeCapability, "no statement "+req.Name+" for "+sc.PluginID)
	}
	if sc.ReadOnly && st.Mode != "read" {
		return nil, refuse(403, CodeCapability, req.Name+" writes; this token reads only")
	}
	switch req.Op {
	case "fetch", "execute", "executemany":
	default:
		return nil, refuse(400, CodeInvalid, "op "+req.Op)
	}
	argSets := [][]json.RawMessage{req.Args}
	if req.Op == "executemany" {
		argSets = req.Rows
	}
	decoded := make([][]any, len(argSets))
	passed := map[Check]map[string]bool{} // what this call already checked (executemany's rows)
	for i, raw := range argSets {
		args, err := decodeArgs(raw)
		if err != nil {
			return nil, refuse(400, CodeInvalid, err.Error())
		}
		if ref := e.checkScopes(ctx, sc, st, args, passed); ref != nil {
			return nil, ref
		}
		decoded[i] = args
	}
	start := time.Now()
	defer func() {
		callsTotal.WithLabelValues(sc.PluginID, st.Name).Inc()
		callSeconds.WithLabelValues(sc.PluginID).Observe(time.Since(start).Seconds())
	}()
	if req.Tx != "" {
		ot, ref := e.tx(sc, req.Tx)
		if ref != nil {
			return nil, ref
		}
		if ref := e.lockTx(ot, req.Tx); ref != nil {
			return nil, ref
		}
		defer e.unlockTx(ot)
		if ot.readOnly && st.Mode != "read" {
			return nil, refuse(403, CodeCapability, "a write in a read-only transaction")
		}
		// bounded: a call that never returns would hold the transaction from the reaper
		sctx, cancel := context.WithTimeout(ctx, e.cfg.TxStmtTimeout)
		defer cancel()
		return run(sctx, ot.tx, st, req, decoded)
	}
	conn, err := e.acquire(ctx, sc)
	if err != nil {
		return nil, dbError(err)
	}
	out, ref := run(ctx, conn, st, req, decoded)
	if ref != nil && ref.badConn && req.Op != "executemany" {
		// a kept connection the database closed meanwhile (it restarted): the statement
		// was not run, so it runs once more on a new connection, as database/sql does for
		// its own pool. The plugin's other kept connections are as old: they go too. Not
		// an executemany, whose earlier rows may be written already.
		e.release(sc, conn, true)
		e.dropIdle(sc)
		if conn, err = e.acquire(ctx, sc); err != nil {
			return nil, dbError(err)
		}
		out, ref = run(ctx, conn, st, req, decoded)
	}
	// a statement's own failure leaves the connection as it was; a lost one is not reused
	e.release(sc, conn, ref != nil && ref.Status == 503)
	return out, ref
}

func run(ctx context.Context, tx querier, st *Statement, req Request, sets [][]any) (*Response, *Error) {
	switch req.Op {
	case "fetch":
		rows, err := tx.QueryContext(ctx, st.Text, sets[0]...)
		if err != nil {
			return nil, dbError(err)
		}
		defer rows.Close()
		out, err := encodeRows(rows, req.Limit)
		if err != nil {
			return nil, dbError(err)
		}
		return out, nil
	case "execute":
		res, err := tx.ExecContext(ctx, st.Text, sets[0]...)
		if err != nil {
			return nil, dbError(err)
		}
		n, _ := res.RowsAffected()
		return &Response{Status: commandTag(st.Text, n)}, nil
	default:
		for _, a := range sets {
			if _, err := tx.ExecContext(ctx, st.Text, a...); err != nil {
				return nil, dbError(err)
			}
		}
		return &Response{}, nil
	}
}

// commandTag is asyncpg's status string for an execute ("UPDATE 3", "INSERT 0 1"): the
// moved code reads the count from it. The verb is the statement's own, after any WITH.
func commandTag(text string, n int64) string {
	verb, depth := "", 0
	word := strings.Builder{}
	flush := func() bool {
		w := strings.ToUpper(word.String())
		word.Reset()
		if depth == 0 && verb == "" {
			switch w {
			case "SELECT", "INSERT", "UPDATE", "DELETE", "MERGE", "VALUES", "TRUNCATE":
				verb = w
				return true
			}
		}
		return false
	}
	for _, r := range text {
		switch r {
		case '(':
			if flush() {
				break
			}
			depth++
		case ')':
			flush()
			depth--
		case ' ', '\n', '\t', '\r', ',':
			flush()
		default:
			word.WriteRune(r)
		}
		if verb != "" {
			break
		}
	}
	if verb == "" {
		flush()
	}
	switch verb {
	case "":
		verb = "SELECT"
	case "INSERT":
		return "INSERT 0 " + strconv.FormatInt(n, 10)
	case "VALUES":
		verb = "SELECT"
	}
	return verb + " " + strconv.FormatInt(n, 10)
}

// dbError maps a database error: a permission refusal (42501) is the plugin's role
// meeting a table outside its manifest, 403 plugin_capability; a connection the database
// closed or refused (it is stopping, starting or restarted) is 503, which the plugin's
// side retries (the job fails as retryable); anything else is the statement's own
// failure, 409 with Postgres's code so the plugin's code sees it.
func dbError(err error) *Error {
	var pe *pq.Error
	if errors.As(err, &pe) {
		if pe.Code == "42501" {
			return refuse(403, CodeCapability, pe.Message)
		}
		if connectionLost(string(pe.Code)) {
			// the session ended: a statement of its own was not run, or was rolled back
			ref := refuse(503, CodeDatabase, string(pe.Code)+": "+pe.Message)
			ref.badConn = true
			return ref
		}
		return refuse(409, CodeDatabase, string(pe.Code)+": "+pe.Message)
	}
	ref := refuse(503, CodeDatabase, err.Error())
	ref.badConn = errors.Is(err, driver.ErrBadConn)
	return ref
}

// connectionLost reports whether a Postgres error code says the connection went, not
// the statement: class 08 (connection exception) and the server's own shutdown codes
// (57P01 admin_shutdown, 57P02 crash_shutdown, 57P03 cannot_connect_now).
func connectionLost(code string) bool {
	return strings.HasPrefix(code, "08") || code == "57P01" || code == "57P02" || code == "57P03"
}

func (e *Executor) tx(sc Scope, id string) (*openTx, *Error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ot := e.txs[id]
	if ot == nil || ot.inv != sc.Invocation {
		return nil, refuse(409, CodeTx, "no open transaction "+id)
	}
	return ot, nil
}

// Tx opens or ends a transaction of an invocation.
func (e *Executor) Tx(ctx context.Context, sc Scope, req TxRequest) (map[string]string, *Error) {
	if req.Op == "begin" {
		e.reap()
		// the transaction outlives this request: it is the invocation's, ended by its own
		// commit or rollback, by EndInvocation, or by the idle reaper
		conn, err := e.acquire(context.WithoutCancel(ctx), sc)
		if err != nil {
			return nil, refuse(503, CodeDatabase, err.Error())
		}
		tx, err := conn.BeginTx(context.WithoutCancel(ctx), &sql.TxOptions{ReadOnly: sc.ReadOnly})
		if err != nil {
			e.discard(conn)
			return nil, refuse(503, CodeDatabase, err.Error())
		}
		id := newID()
		now := e.cfg.Now()
		ot := &openTx{tx: tx, conn: conn, inv: sc.Invocation, begun: now, readOnly: sc.ReadOnly}
		ot.touch(now)
		e.mu.Lock()
		closed := e.closed
		if !closed {
			e.txs[id] = ot
		}
		e.mu.Unlock()
		if closed {
			e.end(ot)
			return nil, refuse(503, CodeDatabase, "the plugin API is stopping")
		}
		return map[string]string{"tx": id}, nil
	}
	ot, ref := e.tx(sc, req.Tx)
	if ref != nil {
		return nil, ref
	}
	if ref := e.lockTx(ot, req.Tx); ref != nil {
		return nil, ref
	}
	defer e.unlockTx(ot)
	sctx, cancel := context.WithTimeout(ctx, e.cfg.TxStmtTimeout)
	defer cancel()
	sp := pq.QuoteIdentifier(req.SP)
	var err error
	switch req.Op {
	case "commit", "rollback":
		e.mu.Lock()
		delete(e.txs, req.Tx)
		e.mu.Unlock()
		if req.Op == "commit" {
			err = ot.tx.Commit()
		} else {
			err = ot.tx.Rollback()
		}
		ot.done = true
		e.release(sc, ot.conn, err != nil)
		if req.Op == "rollback" {
			// a rollback that fails left nothing committed either (a statement that ran past
			// TxStmtTimeout, say, whose cancel closed the connection): it is done
			err = nil
		}
	case "savepoint":
		_, err = ot.tx.ExecContext(sctx, "SAVEPOINT "+sp)
	case "release":
		_, err = ot.tx.ExecContext(sctx, "RELEASE SAVEPOINT "+sp)
	case "rollback_to":
		_, err = ot.tx.ExecContext(sctx, "ROLLBACK TO SAVEPOINT "+sp)
	default:
		return nil, refuse(400, CodeInvalid, "op "+req.Op)
	}
	if err != nil {
		return nil, dbError(err)
	}
	return map[string]string{"tx": req.Tx}, nil
}

// EndInvocation rolls back what an ended invocation left open. It never waits for a call
// that holds one of its transactions: that call rolls it back as it returns (end).
func (e *Executor) EndInvocation(id string) {
	e.mu.Lock()
	var open []*openTx
	for k, ot := range e.txs {
		if ot.inv == id {
			open = append(open, ot)
			delete(e.txs, k)
		}
	}
	delete(e.vlibs, id)
	e.mu.Unlock()
	for _, ot := range open {
		e.end(ot)
	}
}

// Open counts the open transactions (tests, health).
func (e *Executor) Open() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.txs)
}

// legacySettings: a key a rules section still reads from an older release's row. The tv
// rules air the dizqueTV export's live list while they name none of their own (slot 0215
// keeps that row; pipeline rules.legacy_tv_live), until the cleanup migration moves it to
// rules.tv: a plugin that may read the tv rules may read it too.
var legacySettings = map[string]string{"tv": "rules.dizquetv"}

// settingsInScope: key v is one a plugin listing these rules sections may read.
func settingsInScope(v string, sections []string) bool {
	for _, s := range sections {
		if v == "rules."+s || v == "rules_version."+s {
			return true
		}
		if old, ok := legacySettings[s]; ok && v == old {
			return true
		}
	}
	return false
}

// CheckScopes runs step 3 alone, for one call's arguments (tests, and the bundle's walk).
func (e *Executor) CheckScopes(ctx context.Context, sc Scope, st *Statement, args []any) *Error {
	return e.checkScopes(ctx, sc, st, args, map[Check]map[string]bool{})
}

// checkScopes is step 3: each of the statement's checks against the token.
func (e *Executor) checkScopes(ctx context.Context, sc Scope, st *Statement, args []any, passed map[Check]map[string]bool) *Error {
	for _, c := range st.Checks() {
		if c.Arg > len(args) {
			return refuse(400, CodeInvalid, fmt.Sprintf("%s needs argument $%d", st.Name, c.Arg))
		}
		vals, ok := scopeValues(args[c.Arg-1])
		if !ok {
			return refuse(403, CodeScope, fmt.Sprintf("%s: the scope argument $%d is not an id", st.Name, c.Arg))
		}
		for _, v := range vals {
			if passed[c][v] {
				continue
			}
			if ref := e.checkOne(ctx, sc, st, c.Scope, v); ref != nil {
				return ref
			}
			if passed[c] == nil {
				passed[c] = map[string]bool{}
			}
			passed[c][v] = true
		}
	}
	return nil
}

// checkOne checks one value of a scope argument.
func (e *Executor) checkOne(ctx context.Context, sc Scope, st *Statement, kind, v string) *Error {
	switch kind {
	case "settings":
		if settingsInScope(v, sc.Settings) {
			return nil
		}
		return refuse(403, CodeScope, st.Name+": setting "+v+" is not the plugin's")
	case "library":
		// compared as a uuid, never as text: another form of the same id is the same library
		id, perr := uuid.Parse(v)
		if perr != nil {
			return refuse(403, CodeScope, st.Name+": the scope argument is not a library id")
		}
		if sc.LibraryID == "" || sameID(id.String(), sc.LibraryID) {
			return nil
		}
		return refuse(403, CodeScope, st.Name+": library "+id.String())
	case "subject":
		// A video id in any form Postgres takes for a uuid (upper case, no hyphens, braces)
		// names the same row as its canonical text, which the lookup below compares: checked
		// as given, another library's video would miss the lookup and pass as one that is gone.
		id, perr := uuid.Parse(v)
		if perr != nil {
			return refuse(403, CodeScope, st.Name+": the scope argument is not a video id")
		}
		v = id.String()
		if sc.SubjectKind == "video" {
			if sameID(v, sc.SubjectID) {
				return nil
			}
			return refuse(403, CodeScope, st.Name+": video "+v)
		}
		if sc.LibraryID == "" {
			return nil
		}
		if err := e.loadLibrary(ctx, sc); err != nil {
			return refuse(503, CodeDatabase, err.Error())
		}
		lib, err := e.videoLibrary(ctx, sc.Invocation, v)
		if err != nil {
			return refuse(503, CodeDatabase, err.Error())
		}
		if lib == "" || sameID(lib, sc.LibraryID) {
			return nil // a video that is gone matches no row
		}
		return refuse(403, CodeScope, st.Name+": video "+v+" of another library")
	}
	if _, ok := rowKinds[kind]; ok {
		return e.checkRow(ctx, sc, st, kind, v)
	}
	return refuse(400, CodeInvalid, st.Name+": scope "+kind)
}

// scopeValues are the ids one scope argument names: itself, or each element of an array
// (a Postgres array's text, as decodeArg sends one, or a JSON array's: json_each($N)). A
// NULL names none (it matches no row); a value that is not text or a whole number, none.
func scopeValues(a any) ([]string, bool) {
	switch t := a.(type) {
	case nil:
		return nil, true
	case int64:
		return []string{strconv.FormatInt(t, 10)}, true
	case string:
		if strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}") {
			if _, err := uuid.Parse(t); err == nil {
				return []string{t}, true // a braced uuid, not an array
			}
			var arr pq.StringArray
			if err := arr.Scan(t); err != nil {
				return nil, false
			}
			return arr, true
		}
		if strings.HasPrefix(t, "[") {
			var arr []json.Number
			if err := json.Unmarshal([]byte(t), &arr); err == nil {
				out := make([]string, len(arr))
				for i, n := range arr {
					out[i] = n.String()
				}
				return out, true
			}
			var strs []*string
			if err := json.Unmarshal([]byte(t), &strs); err != nil {
				return nil, false
			}
			out := []string{}
			for _, s := range strs {
				if s != nil {
					out = append(out, *s)
				}
			}
			return out, true
		}
		return []string{t}, true
	}
	return nil, false
}

// sameID: two ids name one row, read as uuids (never compared as text, #937).
func sameID(a, b string) bool {
	x, err1 := uuid.Parse(a)
	y, err2 := uuid.Parse(b)
	return err1 == nil && err2 == nil && x == y
}

// videoLibrary answers a video's library for a scope check, from the invocation's cache:
// a library job's videos are read in one query the first time (a stage that writes one row
// per video would otherwise pay a lookup per row).
func (e *Executor) videoLibrary(ctx context.Context, inv, video string) (string, error) {
	e.mu.Lock()
	if lib, ok := e.vlibs[inv][video]; ok {
		e.mu.Unlock()
		return lib, nil
	}
	e.mu.Unlock()
	var lib sql.NullString
	err := e.DB.QueryRowContext(ctx, `SELECT CAST(library_id AS text) FROM videos WHERE CAST(id AS text) = $1`, video).Scan(&lib)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	e.mu.Lock()
	if e.vlibs[inv] == nil {
		e.vlibs[inv] = map[string]string{}
	}
	e.vlibs[inv][video] = lib.String
	e.mu.Unlock()
	return lib.String, nil
}

// loadLibrary fills the invocation's video cache with its library's videos, once.
func (e *Executor) loadLibrary(ctx context.Context, sc Scope) error {
	key := "library:" + sc.LibraryID
	e.mu.Lock()
	if _, done := e.vlibs[sc.Invocation][key]; done {
		e.mu.Unlock()
		return nil
	}
	e.mu.Unlock()
	rows, err := e.DB.QueryContext(ctx, `SELECT CAST(id AS text) FROM videos WHERE CAST(library_id AS text) = $1`, sc.LibraryID)
	if err != nil {
		return err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	m := e.vlibs[sc.Invocation]
	if m == nil {
		m = map[string]string{}
		e.vlibs[sc.Invocation] = m
	}
	for _, id := range ids {
		m[id] = sc.LibraryID
	}
	m[key] = sc.LibraryID
	e.mu.Unlock()
	return nil
}
