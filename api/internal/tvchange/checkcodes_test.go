package tvchange

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange/tvchangetest"
)

// A check's value carries its code and params beside its English (the apps say it in
// their own language; an app that does not know the code shows the English).

func reportCheck(t *testing.T, c *Change, name string) map[string]any {
	t.Helper()
	xs, _ := c.VerifyReport["checks"].([]any)
	for _, x := range xs {
		if m, _ := x.(map[string]any); m["check"] == name {
			return m
		}
	}
	t.Fatalf("no %s in %v", name, c.VerifyReport)
	return nil
}

func lastFailDetail(t *testing.T, w *svWorld, id string) map[string]any {
	t.Helper()
	evs, err := w.ctl.Store.Events(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Action == ActionFail {
			return evs[i].Detail
		}
	}
	t.Fatal("no fail event")
	return nil
}

func TestCheckCodedKeepsItsValue(t *testing.T) {
	k := Check{Check: "guide.coverage", Result: ResultFail, Value: "3 gaps in the next 24 h"}.coded("guide.gaps", "gaps", 3)
	if k.Code != "guide.gaps" || k.Params["gaps"] != "3" || k.Value != "3 gaps in the next 24 h" {
		t.Fatalf("%+v", k)
	}
	if k = k.coded("guide.none"); k.Params != nil || k.Code != "guide.none" {
		t.Fatalf("a recode keeps the old params: %+v", k)
	}
}

// The three ways the staging slot fails to come up each carry their code, in the report
// and on the move; the English Value stays for older apps.
func TestStagingSlotFailureCarriesItsCode(t *testing.T) {
	cases := []struct {
		name, code, value string
		params            map[string]string
		setup             func(w *svWorld) string // the release's streaming version
	}{
		{name: "release_mismatch", code: "slot.release_mismatch",
			value: "the staging slot runs another release (release_mismatch)",
			setup: func(w *svWorld) string {
				healthy := w.fs.view
				w.fs.view = func(p StagingPlan) *StagingView {
					v := healthy(p)
					v.Status.Mismatch = true
					return v
				}
				return "v2"
			}},
		{name: "not_healthy", code: "slot.not_healthy", value: "the staging slot did not become healthy on the release",
			setup: func(*svWorld) string { return "v3" }}, // the fake slot reports v2
		{name: "start_failed", code: "slot.start_failed", value: "the staging slot did not start: front door said no",
			params: map[string]string{"error": "front door said no"},
			setup: func(w *svWorld) string {
				w.fs.err = errors.New("front door said no")
				return "v2"
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			be := tvchangetest.Backends(t)[0]
			w := newSVWorld(t, be.DB)
			w.results = passing()
			c := w.staged(tc.setup(w))
			w.tick()
			w.now = w.now.Add(slotStartTimeout + time.Second)
			w.tick()
			got := w.get(c.ID)
			if got.State != StateDraft {
				t.Fatalf("state %s", got.State)
			}
			k := reportCheck(t, got, "slot.health")
			if k["code"] != tc.code || k["value"] != tc.value || k["result"] != ResultFail {
				t.Fatalf("check %v", k)
			}
			if fmt.Sprint(k["params"]) != fmt.Sprint(mapOrNil(tc.params)) {
				t.Fatalf("params %v, want %v", k["params"], tc.params)
			}
			d := lastFailDetail(t, w, c.ID)
			if d["report"] != tc.value || d["report_code"] != tc.code {
				t.Fatalf("move detail %v", d)
			}
			// the Updates page's summary of a held TV keeps the code
			list := failedCheckList(got.VerifyReport)
			if len(list) != 1 || list[0].Code != tc.code || list[0].Value != tc.value ||
				fmt.Sprint(list[0].Params) != fmt.Sprint(tc.params) {
				t.Fatalf("failed checks %+v", list)
			}
		})
	}
}

// Every value code the controller writes has the web's words in English and Arabic
// (web/src/i18n, tvchanges.value.<code>), which the apps copy: a new code without them
// would show the English everywhere.
func TestEveryCheckCodeHasItsWords(t *testing.T) {
	catalogs := map[string]map[string]string{}
	for _, lang := range []string{"en", "ar"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "src", "i18n", lang+".json"))
		if err != nil {
			t.Skipf("no web catalogs here: %v", err)
		}
		m := map[string]string{}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		catalogs[lang] = m
	}
	re := regexp.MustCompile(`coded\("([a-z_.]+)"|code(?:, kv)? :?= "([a-z_.]+)"|bcode :?= "([a-z_.]+)"`)
	codes := map[string]bool{"position.offset_settling": true}
	for _, f := range []string{"verify.go", "streamverify.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			code := m[1] + m[2] + m[3]
			codes[code] = true
			if strings.HasPrefix(code, "budget.") {
				codes[code+"_rolling"] = true
			}
		}
	}
	if len(codes) < 40 {
		t.Fatalf("found only %d codes: %v", len(codes), codes)
	}
	for code := range codes {
		for lang, m := range catalogs {
			if m["tvchanges.value."+code] == "" {
				t.Errorf("%s: no tvchanges.value.%s", lang, code)
			}
		}
	}
}

func mapOrNil(m map[string]string) any {
	if m == nil {
		return nil
	}
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Every check the controller measures itself carries a code; the streaming software's
// own (ts.conformity, splice.align, its first segment) keep only their value.
func TestVerifiedChecksCarryCodes(t *testing.T) {
	be := tvchangetest.Backends(t)[0]
	w := newSVWorld(t, be.DB)
	w.results = passing()
	c := w.staged("v2")
	w.tick()
	for i := 0; i < 3; i++ {
		w.now = w.now.Add(10 * time.Second)
		w.sampled = w.now
		w.tick()
	}
	got := w.get(c.ID)
	if got.State != StateReady {
		t.Fatalf("after the soak: %s %v", got.State, got.VerifyReport)
	}
	streaming := map[string]bool{"channel.first_segment": true, "ts.conformity": true, "splice.align": true}
	xs, _ := got.VerifyReport["checks"].([]any)
	for _, x := range xs {
		m, _ := x.(map[string]any)
		name, _ := m["check"].(string)
		code, _ := m["code"].(string)
		if streaming[name] != (code == "") {
			t.Errorf("%s: code %q (value %v)", name, code, m["value"])
		}
	}
	if k := reportCheck(t, got, "slot.health"); k["code"] != "slot.healthy" ||
		fmt.Sprint(k["params"]) != "map[pid:42 release:"+short(*got.ReleaseID)+" slot:amber version:v2]" {
		t.Errorf("slot.health %v", k)
	}
	if k := reportCheck(t, got, "encoders.budget"); k["code"] != "budget.ok" {
		t.Errorf("encoders.budget %v", k)
	}
}
