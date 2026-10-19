// Package tvremote is Maktaba's remote viewing through Cloudflare Stream
// (Epic 32). Story 32.6 is the integration: the credentials (account id in
// the cloudflare_stream extension's settings, the API token in
// home_secrets as cf_api_token), "Test connection", the signing key, the
// customer playback domain, the gate on tv_remote.enabled and removing it
// all. Later stories (32.7+) add the input pool, pushes and sessions.
package tvremote

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cf"
)

// Extension is the integration's entry in the extensions list.
const Extension = "cloudflare_stream"

// home_secrets names.
const (
	SecretAPIToken   = "cf_api_token"
	SecretSigningKey = "cf_signing_key"
	// SecretWebhook is the Cloudflare Notifications webhook secret (32.7 AC13).
	SecretWebhook = "cf_webhook_secret"
	// SecretInputPrefix starts the names of the live inputs' stream keys
	// (32.7); removing the integration deletes them too.
	SecretInputPrefix = "cf_input."
)

// app_settings keys.
const (
	SettingEnabled  = "tv_remote.enabled"
	SettingLastTest = "tv_remote.last_test"
	SettingIngest   = "tv_remote.ingest" // legacy; rules.tv_remote ingest wins
	// SettingUnavailable is set when Cloudflare reported an account-level
	// error (storage quota, no subscription): remote is off until a green test.
	SettingUnavailable = "tv_remote.unavailable"
)

// GreenTestMaxAge is how recent a passed test must be to turn remote on (AC4).
const GreenTestMaxAge = 24 * time.Hour

// PoolInputName is the first pool input, created by the test when the
// account has none (its videos show the customer domain; 32.7 reuses it).
const PoolInputName = cf.LiveInputPrefix + "pool-1"

// ErrNeedsGreenTest: remote cannot be turned on without a passed test
// younger than GreenTestMaxAge.
var ErrNeedsGreenTest = errors.New("run Test connection first: remote viewing needs a passed test from the last 24 hours")

// Extensions is what the integration needs from the extensions table (the
// queues handler implements it).
type Extensions interface {
	ExtensionSettings(ctx context.Context, name string) (map[string]any, bool, error)
	SaveExtensionSetting(ctx context.Context, name, key, value string) error
	ClearExtension(ctx context.Context, name string) error
}

// PushAbility is what the home's push relay can push (design §7.4).
type PushAbility struct {
	RTMPS  bool   `json:"rtmps"`
	SRT    bool   `json:"srt"`
	Source string `json:"source"` // streaming_hosts | ffmpeg
}

// Integration is the Cloudflare Stream integration on the home.
type Integration struct {
	DB      *sql.DB
	Secrets secrets.Store
	Ext     Extensions
	// NewClient builds the Cloudflare client (tests: the fake's).
	NewClient func(account, token string) *cf.Client
	Now       func() time.Time
	// CanPush reports the home's push ability; nil: the home's
	// streaming_hosts row (can_push, story 32.1), else the local
	// ffmpeg's protocols.
	CanPush func(ctx context.Context) (PushAbility, error)
	// StopPushes stops every remote push (32.7); nil: none run yet.
	StopPushes func(ctx context.Context) error
	Logger     *slog.Logger
}

func (in *Integration) now() time.Time {
	if in.Now != nil {
		return in.Now().UTC()
	}
	return time.Now().UTC()
}

func (in *Integration) log() *slog.Logger {
	if in.Logger != nil {
		return in.Logger
	}
	return slog.Default()
}

func (in *Integration) client(account, token string) *cf.Client {
	if in.NewClient != nil {
		return in.NewClient(account, token)
	}
	return cf.New(account, token)
}

// Line is one check of Test connection.
type Line struct {
	Key      string `json:"key"` // token | stream | storage | analytics | signing_key | customer_domain | push
	OK       bool   `json:"ok"`
	Optional bool   `json:"optional,omitempty"`
	Skipped  bool   `json:"skipped,omitempty"`
	Detail   string `json:"detail"`
}

// TestResult is POST /api/tv/remote/test's answer, kept in
// app_settings tv_remote.last_test.
type TestResult struct {
	OK        bool      `json:"ok"`
	Message   string    `json:"message"`
	CheckedAt time.Time `json:"checked_at"`
	Lines     []Line    `json:"lines"`
}

// config is the integration's settings and token.
type config struct {
	account  string
	code     string
	token    string
	tokenErr error
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}

func (in *Integration) load(ctx context.Context) (config, error) {
	var c config
	settings, _, err := in.Ext.ExtensionSettings(ctx, Extension)
	if err != nil {
		return c, err
	}
	c.account, c.code = str(settings, "account_id"), cf.NormalizeCustomerCode(str(settings, "customer_code"))
	if in.Secrets == nil {
		c.tokenErr = secrets.ErrNoKey
		return c, nil
	}
	b, err := in.Secrets.Get(ctx, SecretAPIToken)
	if err != nil {
		c.tokenErr = err
		return c, nil
	}
	c.token = strings.TrimSpace(string(b))
	return c, nil
}

func tokenProblem(err error) string {
	switch {
	case errors.Is(err, secrets.ErrNotFound):
		return "no API token saved"
	case errors.Is(err, secrets.ErrWrongKey):
		return "the saved token cannot be opened (secrets.key was replaced): enter the API token again"
	case errors.Is(err, secrets.ErrNoKey):
		return "the secrets store is not available on this server"
	default:
		return err.Error()
	}
}

// Test runs Test connection (story 32.6 AC3, plan §3) and stores the result.
func (in *Integration) Test(ctx context.Context) (TestResult, error) {
	res := TestResult{CheckedAt: in.now()}
	c, err := in.load(ctx)
	if err != nil {
		return res, err
	}
	skip := func(keys ...string) {
		for _, k := range keys {
			res.Lines = append(res.Lines, Line{Key: k, Skipped: true, Optional: k == "analytics", Detail: "not checked"})
		}
	}
	rest := []string{"stream", "storage", "analytics", "signing_key", "customer_domain"}
	switch {
	case c.account == "":
		res.Lines = append(res.Lines, Line{Key: "token", Detail: "no account ID saved"})
		skip(rest...)
	case c.tokenErr != nil:
		res.Lines = append(res.Lines, Line{Key: "token", Detail: tokenProblem(c.tokenErr)})
		skip(rest...)
	default:
		cl := in.client(c.account, c.token)
		in.runChecks(ctx, cl, c, &res)
	}
	res.Lines = append(res.Lines, in.pushLine(ctx))
	res.OK = true
	failed := []string{}
	for _, l := range res.Lines {
		if !l.OK && !l.Optional {
			res.OK = false
			failed = append(failed, l.Key)
		}
	}
	if res.OK {
		res.Message = "Cloudflare Stream is ready"
	} else {
		res.Message = "not ready: " + strings.Join(failed, ", ")
	}
	if err := in.putSetting(ctx, SettingLastTest, res); err != nil {
		return res, err
	}
	if res.OK && in.DB != nil {
		// a green test lifts "remote unavailable" (an account-level Cloudflare error, 32.7 AC11)
		_, _ = in.DB.ExecContext(ctx, `DELETE FROM app_settings WHERE key = $1`, SettingUnavailable)
	}
	in.log().Info("tv_remote: test connection", "ok", res.OK, "failed", strings.Join(failed, ","),
		"event", "tv_remote_test")
	return res, nil
}

func (in *Integration) runChecks(ctx context.Context, cl *cf.Client, c config, res *TestResult) {
	add := func(l Line) { res.Lines = append(res.Lines, l) }
	st, err := cl.VerifyToken(ctx)
	switch {
	case err != nil:
		add(Line{Key: "token", Detail: err.Error()})
		for _, k := range []string{"stream", "storage", "analytics", "signing_key", "customer_domain"} {
			add(Line{Key: k, Skipped: true, Optional: k == "analytics", Detail: "not checked"})
		}
		return
	case st.Status != "active":
		add(Line{Key: "token", Detail: "the token is " + st.Status})
		for _, k := range []string{"stream", "storage", "analytics", "signing_key", "customer_domain"} {
			add(Line{Key: k, Skipped: true, Optional: k == "analytics", Detail: "not checked"})
		}
		return
	default:
		d := "active"
		if st.AccountOwned {
			d = "active (account token)"
		}
		if st.ExpiresOn != "" {
			d += ", expires " + st.ExpiresOn
		}
		add(Line{Key: "token", OK: true, Detail: d})
	}

	inputs, err := cl.ListLiveInputs(ctx)
	streamOK := err == nil
	if err != nil {
		d := err.Error()
		if ae, ok := cf.AsAPIError(err); ok && (ae.Status == http.StatusForbidden || ae.Status == http.StatusUnauthorized) {
			d = "no Stream subscription, or the token lacks Stream permission: " + d
		}
		add(Line{Key: "stream", Detail: d})
	} else {
		mine := 0
		for _, x := range inputs {
			if strings.HasPrefix(x.Name(), cf.LiveInputPrefix) {
				mine++
			}
		}
		add(Line{Key: "stream", OK: true, Detail: fmt.Sprintf("%d live inputs (%d of Maktaba)", len(inputs), mine)})
	}

	if u, err := cl.StorageUsage(ctx); err != nil {
		add(Line{Key: "storage", Detail: err.Error()})
	} else if u.TotalStorageMinutesLimit <= 0 {
		add(Line{Key: "storage", Detail: fmt.Sprintf("no storage capacity (%.0f minutes used): buy a Stream storage plan; live playback records while it runs", u.TotalStorageMinutes)})
	} else {
		add(Line{Key: "storage", OK: true, Detail: fmt.Sprintf("%.0f of %.0f minutes used", u.TotalStorageMinutes, u.TotalStorageMinutesLimit)})
	}

	if _, err := cl.MinutesViewed(ctx, in.now().AddDate(0, 0, -1), 1); err != nil {
		add(Line{Key: "analytics", Optional: true, Detail: "no Account Analytics read (the cost estimate will use session minutes only): " + err.Error()})
	} else {
		add(Line{Key: "analytics", OK: true, Optional: true, Detail: "Account Analytics readable"})
	}

	add(in.signingKeyLine(ctx, cl))

	if c.code != "" {
		add(Line{Key: "customer_domain", OK: true, Detail: "customer-" + c.code + ".cloudflarestream.com"})
	} else if !streamOK {
		add(Line{Key: "customer_domain", Skipped: true, Detail: "not checked"})
	} else {
		add(in.customerLine(ctx, cl, inputs))
	}
}

// storedKey is the cf_signing_key secret.
type storedKey struct {
	ID  string `json:"id"`
	PEM string `json:"pem"`
}

// SigningKey returns the stored signing key (for minting, 32.8).
func (in *Integration) SigningKey(ctx context.Context) (cf.SigningKey, error) {
	if in.Secrets == nil {
		return cf.SigningKey{}, secrets.ErrNoKey
	}
	b, err := in.Secrets.Get(ctx, SecretSigningKey)
	if err != nil {
		return cf.SigningKey{}, err
	}
	var k storedKey
	if err := json.Unmarshal(b, &k); err != nil || k.ID == "" || k.PEM == "" {
		return cf.SigningKey{}, secrets.ErrWrongKey
	}
	return cf.SigningKey{ID: k.ID, PEM: k.PEM}, nil
}

func (in *Integration) signingKeyLine(ctx context.Context, cl *cf.Client) Line {
	k, err := in.SigningKey(ctx)
	if err == nil {
		keys, lerr := cl.ListSigningKeys(ctx)
		if lerr != nil {
			return Line{Key: "signing_key", Detail: lerr.Error()}
		}
		for _, x := range keys {
			if x.ID == k.ID {
				return Line{Key: "signing_key", OK: true, Detail: "key " + k.ID}
			}
		}
		// deleted at Cloudflare: make a new one
	} else if !errors.Is(err, secrets.ErrNotFound) && !errors.Is(err, secrets.ErrWrongKey) {
		return Line{Key: "signing_key", Detail: tokenProblem(err)}
	}
	nk, err := cl.CreateSigningKey(ctx)
	if err != nil {
		return Line{Key: "signing_key", Detail: "cannot create a signing key (the token needs Stream Write): " + err.Error()}
	}
	blob, _ := json.Marshal(storedKey{ID: nk.ID, PEM: nk.PEM})
	if err := in.Secrets.Put(ctx, SecretSigningKey, blob, ""); err != nil {
		return Line{Key: "signing_key", Detail: "created key " + nk.ID + " but could not store it: " + err.Error()}
	}
	return Line{Key: "signing_key", OK: true, Detail: "created key " + nk.ID}
}

func (in *Integration) customerLine(ctx context.Context, cl *cf.Client, inputs []cf.LiveInput) Line {
	learned := func(code, from string) Line {
		if err := in.Ext.SaveExtensionSetting(ctx, Extension, "customer_code", code); err != nil {
			return Line{Key: "customer_domain", Detail: "learned " + code + " but could not save it: " + err.Error()}
		}
		return Line{Key: "customer_domain", OK: true, Detail: "customer-" + code + ".cloudflarestream.com (from " + from + ")"}
	}
	if vs, err := cl.ListVideos(ctx, 5); err == nil {
		for _, v := range vs {
			if code := cf.CustomerCode(v.Playback.HLS); code != "" {
				return learned(code, "a video")
			}
		}
	}
	var pool *cf.LiveInput
	for i := range inputs {
		if inputs[i].Name() == PoolInputName {
			pool = &inputs[i]
			break
		}
	}
	if pool == nil {
		off := false
		created, err := cl.CreateLiveInput(ctx, cf.CreateLiveInput{
			Meta: map[string]any{"name": PoolInputName}, Enabled: &off,
			Recording: &cf.Recording{Mode: "automatic", RequireSignedURLs: true},
		})
		if err != nil {
			return Line{Key: "customer_domain", Detail: "cannot create the first pool input: " + err.Error()}
		}
		pool = &created
	}
	if _, raw, err := cl.GetLiveInput(ctx, pool.UID); err == nil {
		if code := cf.CustomerCode(string(raw)); code != "" {
			return learned(code, "live input "+pool.UID)
		}
	}
	if vs, err := cl.InputVideos(ctx, pool.UID); err == nil {
		for _, v := range vs {
			if code := cf.CustomerCode(v.Playback.HLS); code != "" {
				return learned(code, "live input "+pool.UID)
			}
		}
	}
	return Line{Key: "customer_domain", Detail: "the account shows no playback domain yet: enter the customer subdomain code " +
		"(Stream, Live inputs, the HLS manifest URL customer-<code>.cloudflarestream.com)"}
}

// cfClient is the Cloudflare client and the customer subdomain code.
func (in *Integration) cfClient(ctx context.Context) (*cf.Client, string, error) {
	c, err := in.load(ctx)
	if err != nil {
		return nil, "", err
	}
	if c.account == "" {
		return nil, "", errors.New("no Cloudflare account ID saved")
	}
	if c.tokenErr != nil {
		return nil, "", errors.New(tokenProblem(c.tokenErr))
	}
	return in.client(c.account, c.token), c.code, nil
}

func (in *Integration) ingest(ctx context.Context) string {
	if LoadSettings(ctx, in.DB).Ingest == "srt" {
		return "srt"
	}
	var v string
	if err := in.getSetting(ctx, SettingIngest, &v); err != nil || v == "" {
		return "rtmps"
	}
	return v
}

func (in *Integration) pushLine(ctx context.Context) Line {
	probe := in.CanPush
	if probe == nil {
		probe = in.defaultCanPush
	}
	ab, err := probe(ctx)
	ingest := in.ingest(ctx)
	if err != nil {
		return Line{Key: "push", Detail: "cannot tell whether the home can push: " + err.Error()}
	}
	ok := (ingest == "srt" && ab.SRT) || (ingest != "srt" && ab.RTMPS)
	d := fmt.Sprintf("the home's push relay %s %s (%s)", map[bool]string{true: "can push", false: "cannot push"}[ok], strings.ToUpper(ingest), ab.Source)
	if !ok {
		d += ": its ffmpeg needs TLS (rtmps) or libsrt (srt)"
	}
	return Line{Key: "push", OK: ok, Detail: d}
}

// defaultCanPush reads the home's streaming_hosts.can_push (32.1) and
// falls back to the local ffmpeg's protocol list (the API runs on the home,
// beside the front door's relay).
func (in *Integration) defaultCanPush(ctx context.Context) (PushAbility, error) {
	if in.DB != nil {
		var raw []byte
		err := in.DB.QueryRowContext(ctx,
			`SELECT can_push FROM streaming_hosts WHERE role = 'home' ORDER BY heartbeat_at DESC NULLS LAST LIMIT 1`).Scan(&raw)
		if err == nil && len(raw) > 0 {
			var m map[string]bool
			if json.Unmarshal(raw, &m) == nil {
				return PushAbility{RTMPS: m["rtmps"], SRT: m["srt"], Source: "streaming_hosts"}, nil
			}
		}
	}
	return FFmpegProtocols(ctx, os.Getenv("MAKTABA_FFMPEG"))
}

// FFmpegProtocols runs `ffmpeg -protocols` and reports rtmps / srt output.
func FFmpegProtocols(ctx context.Context, bin string) (PushAbility, error) {
	if bin == "" {
		bin = "ffmpeg"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-hide_banner", "-protocols").Output() //nolint:gosec // configured ffmpeg
	if err != nil {
		return PushAbility{}, fmt.Errorf("ffmpeg -protocols: %w", err)
	}
	return ParseProtocols(string(out)), nil
}

// ParseProtocols reads the Output: section of `ffmpeg -protocols`.
func ParseProtocols(out string) PushAbility {
	ab := PushAbility{Source: "ffmpeg"}
	_, outputs, found := strings.Cut(out, "Output:")
	if !found {
		outputs = out
	}
	for _, l := range strings.Split(outputs, "\n") {
		switch strings.TrimSpace(l) {
		case "rtmps":
			ab.RTMPS = true
		case "srt", "libsrt":
			ab.SRT = true
		}
	}
	return ab
}

// Status is GET /api/tv/remote's integration part.
type Status struct {
	Configured     bool        `json:"configured"`
	AccountID      string      `json:"account_id"`
	TokenSet       bool        `json:"token_set"`
	TokenProblem   *string     `json:"token_problem"`
	SigningKeySet  bool        `json:"signing_key_set"`
	CustomerDomain *string     `json:"customer_domain"`
	Enabled        bool        `json:"enabled"`
	CanEnable      bool        `json:"can_enable"`
	LastTest       *TestResult `json:"last_test"`
}

// Status reports the integration (AC4: the last test time and result).
func (in *Integration) Status(ctx context.Context) (Status, error) {
	var s Status
	c, err := in.load(ctx)
	if err != nil {
		return s, err
	}
	s.AccountID = c.account
	if c.code != "" {
		d := "customer-" + c.code + ".cloudflarestream.com"
		s.CustomerDomain = &d
	}
	if in.Secrets != nil {
		s.TokenSet, _ = in.Secrets.Has(ctx, SecretAPIToken)
		s.SigningKeySet, _ = in.Secrets.Has(ctx, SecretSigningKey)
	}
	if c.tokenErr != nil && !errors.Is(c.tokenErr, secrets.ErrNotFound) {
		p := tokenProblem(c.tokenErr)
		s.TokenProblem = &p
	}
	s.Configured = c.account != "" && c.token != ""
	var last TestResult
	if err := in.getSetting(ctx, SettingLastTest, &last); err == nil && !last.CheckedAt.IsZero() {
		s.LastTest = &last
	}
	_ = in.getSetting(ctx, SettingEnabled, &s.Enabled)
	s.CanEnable = s.Configured && in.greenTest(s.LastTest)
	return s, nil
}

func (in *Integration) greenTest(t *TestResult) bool {
	return t != nil && t.OK && in.now().Sub(t.CheckedAt) < GreenTestMaxAge
}

// SetEnabled switches tv_remote.enabled; on needs a green test younger
// than 24 h (AC4).
func (in *Integration) SetEnabled(ctx context.Context, on bool) error {
	if on {
		st, err := in.Status(ctx)
		if err != nil {
			return err
		}
		if !st.CanEnable {
			return ErrNeedsGreenTest
		}
	}
	return in.putSetting(ctx, SettingEnabled, on)
}

// RemoveResult is what removing the integration did at Cloudflare.
type RemoveResult struct {
	DeletedInputs     []string `json:"deleted_inputs"`
	DeletedSigningKey string   `json:"deleted_signing_key,omitempty"`
	Errors            []string `json:"errors"`
}

// Remove disables remote, stops pushes, deletes Maktaba's live inputs and
// the signing key at Cloudflare, then the secrets and settings (AC5).
// Cloudflare failures are reported; the local removal happens anyway.
func (in *Integration) Remove(ctx context.Context) (RemoveResult, error) {
	res := RemoveResult{DeletedInputs: []string{}, Errors: []string{}}
	if err := in.putSetting(ctx, SettingEnabled, false); err != nil {
		return res, err
	}
	if in.StopPushes != nil {
		if err := in.StopPushes(ctx); err != nil {
			res.Errors = append(res.Errors, "stop pushes: "+err.Error())
		}
	}
	c, err := in.load(ctx)
	if err != nil {
		return res, err
	}
	if c.account != "" && c.tokenErr == nil {
		cl := in.client(c.account, c.token)
		if inputs, err := cl.ListLiveInputs(ctx); err != nil {
			res.Errors = append(res.Errors, "list live inputs: "+err.Error())
		} else {
			for _, x := range inputs {
				if !strings.HasPrefix(x.Name(), cf.LiveInputPrefix) {
					continue
				}
				if err := cl.DeleteLiveInput(ctx, x.UID); err != nil {
					res.Errors = append(res.Errors, "delete live input "+x.UID+": "+err.Error())
					continue
				}
				res.DeletedInputs = append(res.DeletedInputs, x.UID)
			}
		}
		if k, err := in.SigningKey(ctx); err == nil {
			if err := cl.DeleteSigningKey(ctx, k.ID); err != nil {
				res.Errors = append(res.Errors, "delete signing key: "+err.Error())
			} else {
				res.DeletedSigningKey = k.ID
			}
		}
	} else if c.account != "" {
		res.Errors = append(res.Errors, "Cloudflare not cleaned up ("+tokenProblem(c.tokenErr)+"): delete Maktaba's live inputs and signing key in the dashboard")
	}
	if in.Secrets != nil {
		for _, n := range []string{SecretAPIToken, SecretSigningKey, SecretSigningKey + "_prev", SecretWebhook} {
			if err := in.Secrets.Delete(ctx, n); err != nil {
				return res, err
			}
		}
		if in.DB != nil {
			if _, err := in.DB.ExecContext(ctx, `DELETE FROM home_secrets WHERE name LIKE 'cf\_input.%' ESCAPE '\'`); err != nil {
				in.log().Warn("tv_remote: delete input secrets", "err", err, "event", "tv_remote_remove")
			}
		}
	}
	if err := in.Ext.ClearExtension(ctx, Extension); err != nil {
		return res, err
	}
	if in.DB != nil {
		if _, err := in.DB.ExecContext(ctx, `DELETE FROM app_settings WHERE key = $1`, SettingLastTest); err != nil {
			return res, err
		}
	}
	in.log().Info("tv_remote: integration removed", "inputs", len(res.DeletedInputs),
		"errors", len(res.Errors), "event", "tv_remote_removed")
	return res, nil
}

// ValidateSettings refuses a customer code that is not one, and an account
// change while Maktaba has inputs or a signing key on the current account
// (EC2): remove the integration first.
func (in *Integration) ValidateSettings(ctx context.Context, cur, next map[string]any, _ []string) error {
	if code := str(next, "customer_code"); code != "" && cf.NormalizeCustomerCode(code) == "" {
		return httperror.BadRequest("customer_code: enter the customer subdomain code, the <code> of " +
			"customer-<code>.cloudflarestream.com (lower-case letters and digits)")
	}
	was, now := str(cur, "account_id"), str(next, "account_id")
	if was == "" || was == now {
		return nil
	}
	has := false
	if in.Secrets != nil {
		has, _ = in.Secrets.Has(ctx, SecretSigningKey)
	}
	if has {
		return httperror.Conflict("https://maktaba.dev/problems/cloudflare-account-in-use",
			"Maktaba has live inputs and a signing key on account "+was+": remove the integration first")
	}
	return nil
}

func (in *Integration) getSetting(ctx context.Context, key string, out any) error {
	if in.DB == nil {
		return sql.ErrNoRows
	}
	var raw []byte
	if err := in.DB.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, key).Scan(&raw); err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func (in *Integration) putSetting(ctx context.Context, key string, v any) error {
	if in.DB == nil {
		return errors.New("tvremote: no database")
	}
	blob, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = in.DB.ExecContext(ctx, `
INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, $3)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
		key, string(blob), in.now())
	return err
}
