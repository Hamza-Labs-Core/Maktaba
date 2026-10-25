package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"
)

type fakeCerts struct{ node, csr string }

func (f *fakeCerts) IssueCert(_ context.Context, nodeID, csrPEM string) (CertIssue, error) {
	f.node, f.csr = nodeID, csrPEM
	if csrPEM == "bad" {
		return CertIssue{}, errors.New("not a certificate request")
	}
	return CertIssue{Cert: "CERT", CACert: "CA", CASHA256: "ab", NotAfter: time.Unix(0, 0).UTC()}, nil
}

func nextFrame(t *testing.T, s *Session) map[string]any {
	t.Helper()
	select {
	case f := <-s.out:
		var m map[string]any
		if err := json.Unmarshal(f.payload, &m); err != nil {
			t.Fatal(err)
		}
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no frame")
	}
	return nil
}

// A machine joined before the cluster link gets its certificate on its channel session
// (story 35.16 AC3): no re-join. A home process cannot ask.
func TestCertCSROverV2SessionNoRejoin(t *testing.T) {
	certs := &fakeCerts{}
	h := &Hub{cfg: Config{Certs: certs, Logger: slog.Default()}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Session{Kind: "node", NodeID: "0b6f3c1e-2a4d-4e8f-9c71-5d2a8b3e6f10", out: make(chan outFrame, 8), ctx: ctx}
	h.dispatch(s, "cert.csr", []byte(`{"t":"cert.csr","id":"c1","csr":"PEM"}`))
	m := nextFrame(t, s)
	if m["t"] != "cert.issued" || m["id"] != "c1" || m["cert"] != "CERT" || m["ca_cert"] != "CA" || certs.node != s.NodeID {
		t.Fatalf("%v (asked for %q)", m, certs.node)
	}
	h.dispatch(s, "cert.csr", []byte(`{"t":"cert.csr","id":"c2","csr":"bad"}`))
	if m := nextFrame(t, s); m["t"] != "cert.refused" || m["id"] != "c2" {
		t.Fatalf("a bad request: %v", m)
	}
	home := &Session{Kind: "home", out: make(chan outFrame, 8), ctx: ctx}
	h.dispatch(home, "cert.csr", []byte(`{"t":"cert.csr","id":"c3","csr":"PEM"}`))
	if m := nextFrame(t, home); m["t"] != "cert.refused" {
		t.Fatalf("a home process: %v", m)
	}
}

// With the link required, a machine's session off the link may only ask for its
// certificate; everything else it sends is dropped.
func TestCertOnlySessionInRequire(t *testing.T) {
	h := &Hub{cfg: Config{Certs: &fakeCerts{}, Logger: slog.Default()}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Session{Kind: "node", NodeID: "0b6f3c1e-2a4d-4e8f-9c71-5d2a8b3e6f10", out: make(chan outFrame, 8), ctx: ctx,
		certOnly: true}
	h.dispatch(s, "update.state", []byte(`{"t":"update.state","version":"1.5.0"}`))
	if s.updateState != nil {
		t.Fatal("a cert-only session's update.state was taken")
	}
	h.dispatch(s, "cert.csr", []byte(`{"t":"cert.csr","id":"c1","csr":"PEM"}`))
	if m := nextFrame(t, s); m["t"] != "cert.issued" {
		t.Fatalf("cert.csr on a cert-only session: %v", m)
	}
}
