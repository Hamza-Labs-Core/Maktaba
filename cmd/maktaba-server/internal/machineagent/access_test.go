package machineagent

import (
	"encoding/json"
	"testing"
)

// A machine asks for its database access on the session it is connected on (never one it
// is not), with its last login; the home's answer reaches OnAccessAnswer.
func TestRequestAccessAndItsAnswer(t *testing.T) {
	var got []map[string]any
	var answers []AccessAnswer
	a := &Agent{OnAccessAnswer: func(x AccessAnswer) { answers = append(answers, x) }}
	if a.RequestAccess(&HomeDB{Code: "refused", Fails: 3}) {
		t.Fatal("asked with no session")
	}
	a.send = func(v any) {
		b, _ := json.Marshal(v)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		got = append(got, m)
	}
	if !a.RequestAccess(&HomeDB{Code: "refused", Fails: 3, Target: "192.168.50.177:5432/maktaba"}) {
		t.Fatal("not asked on its session")
	}
	db, _ := got[0]["home_db"].(map[string]any)
	if len(got) != 1 || got[0]["t"] != "access.request" || got[0]["id"] == "" || db["code"] != "refused" ||
		db["target"] != "192.168.50.177:5432/maktaba" {
		t.Fatalf("sent %v", got)
	}
	a.dispatch([]byte(`{"t":"access.answer","id":"access-2","ok":false,"code":"cannot_alter_role",`+
		`"reason":"this home's database user may not change this machine's role"}`), a.send)
	if len(answers) != 1 || answers[0].OK || answers[0].Code != "cannot_alter_role" || answers[0].At.IsZero() {
		t.Fatalf("answers %+v", answers)
	}
}
