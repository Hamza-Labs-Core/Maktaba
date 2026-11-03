package cluster

import (
	"net/http"
	"slices"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Epic 51 story 51.12: the approve route hands the chosen components to the components
// before the machine is approved; a stage switch is followed by the component switches.

type fakeComponents struct {
	approved map[string][]string
	switched []map[string]*bool
	refuse   bool
}

func (f *fakeComponents) Approve(_ *http.Request, nodeID string, keep []string) error {
	if f.refuse {
		return (&httperror.Error{Status: http.StatusBadRequest, Title: "unknown component"}).With("code", "unknown_component")
	}
	if f.approved == nil {
		f.approved = map[string][]string{}
	}
	f.approved[nodeID] = keep
	return nil
}

func (f *fakeComponents) StagesSwitched(_ *http.Request, _ string, stages map[string]*bool) error {
	f.switched = append(f.switched, stages)
	return nil
}

// T09/T11 (API half): approve with components passes the ticked list on, before the
// machine is approved; without the field the machine follows the home.
func TestApproveWithComponents(t *testing.T) {
	f := newFixture(t)
	fc := &fakeComponents{}
	f.h.Components = fc
	f.openJoins()
	jr := f.join()
	// a wrong code chooses nothing
	problemOf(t, f.do("POST", "/api/cluster/nodes/"+nodeID+"/approve", `{"code":"`+wrongCode(nodeCode(t, jr.HomePubkey))+`","components":[]}`, admin()), 400, "wrong_code")
	if len(fc.approved) != 0 {
		t.Fatalf("a wrong code chose %v", fc.approved)
	}
	decode(t, f.do("POST", "/api/cluster/nodes/"+nodeID+"/approve",
		`{"code":"`+nodeCode(t, jr.HomePubkey)+`","components":["transcription","subtitles"]}`, admin()), 200, nil)
	if keep, ok := fc.approved[nodeID]; !ok || !slices.Equal(keep, []string{"transcription", "subtitles"}) {
		t.Fatalf("chosen %v", fc.approved)
	}
}

func TestApproveWithoutComponentsFollowsTheHome(t *testing.T) {
	f := newFixture(t)
	fc := &fakeComponents{}
	f.h.Components = fc
	f.openJoins()
	jr := f.join()
	decode(t, f.approve(nodeCode(t, jr.HomePubkey)), 200, nil)
	if len(fc.approved) != 0 {
		t.Fatalf("chosen without asking: %v", fc.approved)
	}
}

// A refused choice is answered as it is, and the machine stays pending; a home with no
// components side refuses a choice.
func TestApproveChoiceRefused(t *testing.T) {
	f := newFixture(t)
	f.h.Components = &fakeComponents{refuse: true}
	f.openJoins()
	jr := f.join()
	body := `{"code":"` + nodeCode(t, jr.HomePubkey) + `","components":["nope"]}`
	problemOf(t, f.do("POST", "/api/cluster/nodes/"+nodeID+"/approve", body, admin()), 400, "unknown_component")
	if n, _ := f.store.Node(t.Context(), nodeID); n.State != "pending" {
		t.Fatalf("state %s after a refused choice", n.State)
	}
	f.h.Components = nil
	problemOf(t, f.do("POST", "/api/cluster/nodes/"+nodeID+"/approve", body, admin()), 409, "components_unavailable")
}

// A stage switch is handed to the components after it is written.
func TestStageSwitchReachesTheComponents(t *testing.T) {
	f := newFixture(t)
	fc := &fakeComponents{}
	f.h.Components = fc
	f.openJoins()
	jr := f.join()
	decode(t, f.approve(nodeCode(t, jr.HomePubkey)), 200, nil)
	decode(t, f.do("PUT", "/api/cluster/nodes/"+nodeID, `{"name":"renamed"}`, admin()), 200, nil)
	decode(t, f.do("PUT", "/api/cluster/nodes/"+nodeID, `{"stages":{"transcribe":true}}`, admin()), 200, nil)
	if len(fc.switched) != 1 || fc.switched[0]["transcribe"] == nil || !*fc.switched[0]["transcribe"] {
		t.Fatalf("switched %v", fc.switched)
	}
}
