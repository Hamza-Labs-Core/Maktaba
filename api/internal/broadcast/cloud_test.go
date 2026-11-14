package broadcast

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The relay agent's takedown routes (story 50.24): the signed text as it came, the ack's body,
// the cloud's JSON passed on, a refusal kept with its status; and the ingest-token request names
// the channel's cloud id when the home knows it.
func TestAgentModerationRoutes(t *testing.T) {
	var got = map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		path := strings.TrimPrefix(r.URL.Path, "/admin/cloud-link/broadcast/")
		got[r.Method+" "+path] = string(b)
		switch path {
		case "takedowns":
			_, _ = io.WriteString(w, "mkbc1.body.sig")
		case "takedowns/a-1/ack":
			w.WriteHeader(http.StatusNoContent)
		case "moderation":
			_, _ = io.WriteString(w, `{"strikes":1}`)
		case "moderation/counter-notices":
			if strings.Contains(string(b), `"action_id":"gone"`) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"error":"not_found"}`)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"c-1"}`)
		case "ingest-token":
			_, _ = io.WriteString(w, `{"token":"mkbi1.x"}`)
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	a := &Agent{Addr: strings.TrimPrefix(srv.URL, "http://"), CloudID: func(_ context.Context, ch string) string {
		if ch == "local-1" {
			return "cloud-1"
		}
		return ""
	}}

	if text, err := a.Takedowns(ctx); err != nil || text != "mkbc1.body.sig" {
		t.Fatalf("takedowns %q %v", text, err)
	}
	if err := a.AckTakedown(ctx, "a-1", TakedownAck{Result: "applied", ItemRef: "v-1"}); err != nil ||
		!strings.Contains(got["POST takedowns/a-1/ack"], `"result":"applied"`) {
		t.Fatalf("ack %v %q", err, got["POST takedowns/a-1/ack"])
	}
	if raw, err := a.Moderation(ctx); err != nil || string(raw) != `{"strikes":1}` {
		t.Fatalf("moderation %s %v", raw, err)
	}
	if raw, err := a.CounterNotice(ctx, json.RawMessage(`{"action_id":"a-1"}`)); err != nil || string(raw) != `{"id":"c-1"}` {
		t.Fatalf("counter-notice %s %v", raw, err)
	}
	var ce *CloudError
	if _, err := a.CounterNotice(ctx, json.RawMessage(`{"action_id":"gone"}`)); !errors.As(err, &ce) || ce.Status != http.StatusNotFound {
		t.Fatalf("a refused counter-notice: %v", err)
	}

	if _, err := a.IngestToken(ctx, "local-1", []string{"720p"}, nil); err != nil || !strings.Contains(got["POST ingest-token"], `"cloud_id":"cloud-1"`) {
		t.Fatalf("the ingest-token request with a cloud id: %v %q", err, got["POST ingest-token"])
	}
	if _, err := a.IngestToken(ctx, "local-2", []string{"720p"}, nil); err != nil || strings.Contains(got["POST ingest-token"], "cloud_id") {
		t.Fatalf("the ingest-token request without one: %v %q", err, got["POST ingest-token"])
	}

	// the relay away: the takedown pull says so
	srv.Close()
	if _, err := a.Takedowns(ctx); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("the relay away: %v", err)
	}
}
