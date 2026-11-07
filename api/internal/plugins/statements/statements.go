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
//  3. the statement's scope argument is in the token's scope: subject $N the job's video
//     (or a video of its library), library $N the job's library, settings $N the key of a
//     rules section the manifest lists (else 403 plugin_scope);
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
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

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
	Name     string   `json:"name"`
	Mode     string   `json:"mode"`
	Scope    string   `json:"scope"`
	ScopeArg int      `json:"scope_arg"`
	Reads    []string `json:"reads"`
	Writes   []string `json:"writes"`
	Text     string   `json:"text"`
	SHA256   string   `json:"sha256"`
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
	tx       *sql.Tx
	inv      string
	mu       sync.Mutex
	lastUsed time.Time
	readOnly bool
}

// Executor runs registered statements for the plugin API.
type Executor struct {
	DB  *sql.DB
	Now func() time.Time

	mu    sync.Mutex
	reg   map[string]map[string]*Statement
	txs   map[string]*openTx
	vlibs map[string]map[string]string // invocation -> video -> library
}

// New answers an executor with no statements registered.
func New(db *sql.DB) *Executor {
	return &Executor{DB: db, Now: time.Now, reg: map[string]map[string]*Statement{},
		txs: map[string]*openTx{}, vlibs: map[string]map[string]string{}}
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
	for i, raw := range argSets {
		args, err := decodeArgs(raw)
		if err != nil {
			return nil, refuse(400, CodeInvalid, err.Error())
		}
		if ref := e.checkScope(ctx, sc, st, args); ref != nil {
			return nil, ref
		}
		decoded[i] = args
	}
	start := e.Now()
	defer func() {
		callsTotal.WithLabelValues(sc.PluginID, st.Name).Inc()
		callSeconds.WithLabelValues(sc.PluginID).Observe(time.Since(start).Seconds())
	}()
	if req.Tx != "" {
		ot, ref := e.tx(sc, req.Tx)
		if ref != nil {
			return nil, ref
		}
		ot.mu.Lock()
		defer ot.mu.Unlock()
		ot.lastUsed = e.Now()
		if ot.readOnly && st.Mode != "read" {
			return nil, refuse(403, CodeCapability, "a write in a read-only transaction")
		}
		return run(ctx, ot.tx, st, req, decoded)
	}
	tx, err := e.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: st.Mode == "read"})
	if err != nil {
		return nil, refuse(503, CodeDatabase, err.Error())
	}
	defer func() { _ = tx.Rollback() }()
	if err := enter(ctx, tx, sc); err != nil {
		return nil, dbError(err)
	}
	out, ref := run(ctx, tx, st, req, decoded)
	if ref != nil {
		return nil, ref
	}
	if err := tx.Commit(); err != nil {
		return nil, dbError(err)
	}
	return out, nil
}

// enter makes a transaction the plugin's: its role and its attribution (54.24 §2 step 4).
func enter(ctx context.Context, tx *sql.Tx, sc Scope) error {
	if _, err := tx.ExecContext(ctx, "SET LOCAL ROLE "+pq.QuoteIdentifier(Role(sc.PluginID))); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "SELECT set_config('maktaba.origin', $1, true)", "plugin:"+sc.PluginID+"@"+sc.Version)
	return err
}

func run(ctx context.Context, tx *sql.Tx, st *Statement, req Request, sets [][]any) (*Response, *Error) {
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
		switch {
		case r == '(':
			if flush() {
				break
			}
			depth++
		case r == ')':
			flush()
			depth--
		case r == ' ' || r == '\n' || r == '\t' || r == '\r' || r == ',':
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
// meeting a table outside its manifest, 403 plugin_capability; anything else is the
// statement's own failure, answered with Postgres's code so the plugin's code sees it.
func dbError(err error) *Error {
	var pe *pq.Error
	if errors.As(err, &pe) {
		if pe.Code == "42501" {
			return refuse(403, CodeCapability, pe.Message)
		}
		return refuse(409, CodeDatabase, string(pe.Code)+": "+pe.Message)
	}
	return refuse(503, CodeDatabase, err.Error())
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
		tx, err := e.DB.BeginTx(context.WithoutCancel(ctx), &sql.TxOptions{ReadOnly: sc.ReadOnly})
		if err != nil {
			return nil, refuse(503, CodeDatabase, err.Error())
		}
		if err := enter(ctx, tx, sc); err != nil {
			_ = tx.Rollback()
			return nil, dbError(err)
		}
		id := newID()
		e.mu.Lock()
		e.txs[id] = &openTx{tx: tx, inv: sc.Invocation, lastUsed: e.Now(), readOnly: sc.ReadOnly}
		e.mu.Unlock()
		return map[string]string{"tx": id}, nil
	}
	ot, ref := e.tx(sc, req.Tx)
	if ref != nil {
		return nil, ref
	}
	ot.mu.Lock()
	defer ot.mu.Unlock()
	ot.lastUsed = e.Now()
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
	case "savepoint":
		_, err = ot.tx.ExecContext(ctx, "SAVEPOINT "+sp)
	case "release":
		_, err = ot.tx.ExecContext(ctx, "RELEASE SAVEPOINT "+sp)
	case "rollback_to":
		_, err = ot.tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+sp)
	default:
		return nil, refuse(400, CodeInvalid, "op "+req.Op)
	}
	if err != nil {
		return nil, dbError(err)
	}
	return map[string]string{"tx": req.Tx}, nil
}

// EndInvocation rolls back what an ended invocation left open.
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
		ot.mu.Lock()
		_ = ot.tx.Rollback()
		ot.mu.Unlock()
	}
}

// reap rolls back transactions idle past TxIdle.
func (e *Executor) reap() {
	now := e.Now()
	e.mu.Lock()
	var old []*openTx
	for k, ot := range e.txs {
		if now.Sub(ot.lastUsed) > TxIdle && ot.mu.TryLock() {
			old = append(old, ot)
			delete(e.txs, k)
		}
	}
	e.mu.Unlock()
	for _, ot := range old {
		_ = ot.tx.Rollback()
		ot.mu.Unlock()
	}
}

// Open counts the open transactions (tests, health).
func (e *Executor) Open() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.txs)
}

// checkScope is step 3: the statement's scope argument against the token.
func (e *Executor) checkScope(ctx context.Context, sc Scope, st *Statement, args []any) *Error {
	if st.Scope == "catalog" {
		return nil
	}
	if st.ScopeArg < 1 || st.ScopeArg > len(args) {
		return refuse(400, CodeInvalid, fmt.Sprintf("%s needs argument $%d", st.Name, st.ScopeArg))
	}
	v, ok := args[st.ScopeArg-1].(string)
	if !ok {
		if args[st.ScopeArg-1] == nil {
			return nil // a NULL matches no row
		}
		return refuse(403, CodeScope, st.Name+": the scope argument is not an id")
	}
	switch st.Scope {
	case "settings":
		for _, s := range sc.Settings {
			if v == "rules."+s || v == "rules_version."+s {
				return nil
			}
		}
		return refuse(403, CodeScope, st.Name+": setting "+v+" is not the plugin's")
	case "library":
		if sc.LibraryID == "" || strings.EqualFold(v, sc.LibraryID) {
			return nil
		}
		return refuse(403, CodeScope, st.Name+": library "+v)
	case "subject":
		if sc.SubjectKind == "video" {
			if strings.EqualFold(v, sc.SubjectID) {
				return nil
			}
			return refuse(403, CodeScope, st.Name+": video "+v)
		}
		if sc.LibraryID == "" {
			return nil
		}
		lib, err := e.videoLibrary(ctx, sc.Invocation, v)
		if err != nil {
			return refuse(503, CodeDatabase, err.Error())
		}
		if lib == "" || strings.EqualFold(lib, sc.LibraryID) {
			return nil // a video that is gone matches no row
		}
		return refuse(403, CodeScope, st.Name+": video "+v+" of another library")
	}
	return refuse(400, CodeInvalid, st.Name+": scope "+st.Scope)
}

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
